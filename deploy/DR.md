# 灾备方案(DR)

> 本文件是 [issue #9](../.github/ISSUE_TEMPLATE) 讨论后定稿的落地方案。**尚未实施**,下面是设计、脚本清单与分阶段验收。
> 目标:RPO ≤ 15min、RTO ≤ 2min、客户端零改动、不停机;入口层与家主机都能扛单点故障。

## 1. 现状(实测)

| 项 | 实测 |
|---|---|
| 家主机 `lab` = `192.168.0.202` | 2C / 5.4G(空闲 ~4.5G)/ 48G 盘(剩 32G);跑 ai-gateway + piks + `frpc-gateway`;**无 `sqlite3`、无 `age`**,有 `openssl` |
| 云主机 `aliyun` = `47.116.65.140` | 2C / 1.6G(可用 ~1.2G)/ 40G 盘(剩 32G);跑 `frps` + `litesentry`;无 Docker Hub(走 daocloud 镜像源) |
| frps | `bindPort=17000`,`allowPorts 17001–17100`。**17080/17090 是 frps 动态占用的中继口** —— home 的 frpc 一断,frps 自动释放 |
| 客户端入口 | 裸 `IP:端口`(`47.116.65.140:17080|17090`),证书自签,叶子 SAN **已含公网 IP** |
| 备份 | home `crontab` 里**没有**网关备份(piks 有);`~/gw-backups/` 几份是手动快照,**同机 = 挡不住主机故障** |
| 主密钥 | home `~/ai-gateway/.env` 的 `GW_MASTER_KEY`,**全系统唯一不可再生**;云上旧栈那把无关 |

### 关键观察

- **云主机与 frps 同一个公网 IP**。所以「切到云」时**客户端连的还是 `47.116.65.140:17080/17090`,证书 SAN 也对得上** —— 客户端零改动、不用换 IP、**不需要域名**(域名只在「换入口 IP」时才必需)。
- **frps 的 remotePort 独占**,端口本身就是「谁在服务」的仲裁者 —— 自动接管靠它天然防脑裂(见 §3)。
- 家主机没 `sqlite3`,现有 `deploy/scripts/backup.sh` 只能「停容器几秒再拷」。要做**在线零停机**快照,需一个静态 Go 小命令(见 §4)。

## 2. 定稿架构

```
正常:  客户端 → 47.116.65.140:17080/17090 → frps → frpc(home) → home 容器
                                                     ↑ 独占 17080/17090
home 每 2min : 心跳 push ─┐
home 每 15min: 加密快照 push ─┴→ 云 ~/ai-gateway-standby/

故障:  home 心跳停 → 云控制脚本判定失联 → 云容器直接绑 17080/17090 接管(同 IP)
恢复:  home 心跳恢复 → 云容器让位 → home frpc 抢回端口 → 服务回到 home
```

- **RTO**:手动 `failover.sh` < 2min;自动接管(§3)为「心跳超时阈值 + 1min cron」。
- **RPO** = 最近一次快照年龄,配置为 **15min**。
- 云上所需的全部:同 `GW_MASTER_KEY` 的 `.env`、`docker-compose.yml`、`certs/{admin,api}/{fullchain,key}.pem` + `ca.crt`、最新加密快照、`ai-gateway:<tag>` 镜像。镜像 45MB、DB 2.7MB,云盘 32G 富余。
- ⚠️ **云上只放叶子证书 + `ca.crt`,绝不放 `ca.key`**(CA 私钥只留本地/离线)。加密快照的口令 `GW_BACKUP_KEY` 与 `GW_MASTER_KEY` 一样进密码管理器;云上 `.env` 是唯一另一处持有主密钥的地方 —— 这正是「异地能救回来」的前提。

## 3. 自动接管(不脑裂)

归属判据**只看一个信号** —— 云上 `heartbeat` 文件的鲜度(`< 6min` = home 活着),由云 cron(1min)驱动:

- 心跳新鲜 → 云**释放**端口(`compose stop`)。
- 心跳过期 → 云**尝试**占用端口(`compose up -d`)。

**判错的两个方向都无害**:

- 误判「home 死了」但其实活着 → 端口被 home 的 frpc 占着,云容器**绑不上、起不来** → 客户端照常走 home。
- 误判「home 活着」但其实死了 → 心跳过期这条兜住,下一轮接管。

心跳走**独立通道**(home 定时 push 到云),所以「home 恢复」不依赖隧道本身 —— 避免「隧道抢不回端口就永远回不来」的死锁。

home 侧再加**看门狗**:容器不健康即 `compose restart`(治理「主机活着但网关进程坏了」,在源头自愈)。

**切回的数据分叉(以云为准)**:云接管期会写入新数据(令牌/日志)。home 恢复时,云容器**停之前**先 `gwsnap` 出 `handback.db` + 写标记 → 让位;home 巡检发现标记 → 把 `handback.db` 拉回替换本地库 → 重启。代价:home 断电前最后 ≤15min 的写入丢弃(落在 RPO 内),换云上故障期数据不丢。

## 4. 要新增的文件

| 文件 | 位置 | 作用 |
|---|---|---|
| `cmd/gwsnap` | 仓库(Go 静态命令) | 用 `modernc.org/sqlite` 跑 `VACUUM INTO`,**在线一致快照、零停机**;复用 `cmd/gwbackfill` 的「交叉编译 → scp 过去 → 现成镜像里跑」套路 |
| `deploy/scripts/backup-push.sh` | home cron | 快照 → `openssl enc -aes-256-cbc -pbkdf2` 加密 → 推云 + 写心跳 |
| `deploy/scripts/controller.sh` | 云 cron(1min) | 按心跳鲜度控制云容器启停(自动接管 / 让位) |
| `deploy/scripts/failover.sh` | 本地/云 | 手动一键切(首次预置、手动兜底、演练) |
| `deploy/failover.env` | **不入库**(`.gitignore`) | 唯一配置点:`HOME_HOST / CLOUD_A / CLOUD_B(可选) / PUBLIC_DOMAIN(可选) / 端口 / 快照目标 / KEEP / 心跳阈值` |

**防返工**:所有脚本**不写死 IP 与端口**,统一读 `deploy/failover.env`;投递目标走「列表」(现在 1 个,加云就多一行);云上备机目录按「可能有多份」组织(`standby-a/`、`standby-b/`)。

## 5. 未来扩展(已预留,不在本期做)

- **加第二台云 = 第二入口(frps)**:A、B 各跑 frps;home 的 frpc `frpc.toml` **同时**连两个 frps(同名的 17080/17090 在不同机器上不冲突);客户端用**域名**选路。云 A 挂 → DNS 切 B,TTL 60s 生效。这是第一次能扛「云主机故障」这一档。
- **域名**:客户端只把 `ANTHROPIC_BASE_URL` / 网关设置「对外基址」由裸 IP 换成域名一次,以后不再碰;家主机断电这档(tunnel 层)**不用动 DNS**。证书**继续自签**(被 frps 中转的非标端口没法走 LE 的 HTTP-01;CA 不变时**只 RESIGN 加 SAN,客户端零改动**)。落地点:`gen-certs.sh` 的 SAN 已支持传 DNS 列表(`GW_DNS=…`),加域名时 `RESIGN=1` 重签 + 重推即可。

## 6. 分阶段与验收

| 阶段 | 内容 | 验收 |
|---|---|---|
| **P0** | ① `GW_MASTER_KEY` 抄进密码管理器(离线);② home 接上本地快照 cron(先落 `~/gw-backups/`) | 密钥能取回;本机列出近 N 份快照 |
| **P1** | `cmd/gwsnap` + 加密 + 建 home→云投递通道 + 云上快照轮转 | 云上见近 3 天加密快照,能解密打开 |
| **P2** | 云上备机目录预置(`.env`/compose/certs/镜像)+ `failover.sh`,手动演练一次 | 云上起服务 `/healthz` 通、能登录、数据完整 |
| **P3** | 心跳 + `controller.sh` 自动接管/让位 + 切回对账(`handback.db`) | 拔家主机网/电 → 自动切;恢复 → 自动回 |
| **P4** | 真断一次演练 | 客户端全程零改动,RTO 记录 ≤2min |

### 指标定义

- **RPO** = 最近一次异地快照的年龄(目标 ≤15min)。
- **RTO** = 从决定切换到服务恢复(手动目标 ≤2min;自动 = 心跳阈值 + ≤1min cron)。

## 7. 已知坑

- **frp 端口独占**:切之前必须先让原 frpc 让出端口(自动接管已由心跳驱动规避)。
- **数据分叉**:快照是某时间点,切换会丢掉最后一次快照之后的写入(§3 以云为准反向拉回)。
- **证书 SAN 与域名**:用域名访问必须让证书匹配域名,否则客户端 TLS 校验失败。CA 不变时只 RESIGN,客户端无需重配。
- **异地快照敏感性**:DB 含令牌 hash 与渠道密钥密文(密文需 `GW_MASTER_KEY` 才能解),「快照 + 主密钥」必须分开存放,**不能同放一台备机**;快照落云前必须加密。
- **home → 云暂无 ssh 通路**:202 上没配别名/密钥。P1 会在 home 生成一把**专用 ed25519 密钥**(仅用于投递快照/心跳),公钥追加到云 `authorized_keys`。

## 参考

`deploy/scripts/backup.sh`、`deploy/scripts/deploy.sh`、`deploy/scripts/setup-frp.sh`、`deploy/docker-compose.yml`、`deploy/config.prod.yaml`、`README.md`。
