# RECOVERY.md — 网关故障 / 升级异常 应急手册

> **什么时候读它**：网关升级出事、服务起不来、或需要切到备用机时。
> **设计原则**：所有动作都走 **`ssh` 直连主机**（不经网关）——这样**网关挂了也能照做**，不依赖网关、不依赖 AI。
> **复制即用**：每段都是可整段粘贴的命令（本机执行，`ssh <主机> '…'` 形式）。
> **本手册对应「SQLite 时期」**；`HA.md` 的 P2 迁移到 PostgreSQL 后,数据相关命令另更新。
>
> 相关：`deploy/HA.md`（总方案）、`deploy/README.md`（部署/升级/回滚）、`deploy/scripts/`。

---

## 0. 事实速查（2026-10-08 实测）

| 项 | 值 |
|---|---|
| 生产主机 | `aliyun` = `47.116.65.140`（root，`ssh aliyun`） |
| 工程目录 | `/opt/ai-gateway-v2` |
| compose 服务名 / 容器 | `gateway` / `ai-gateway-v2-gateway-1` |
| 库文件 | `/opt/ai-gateway-v2/data/gateway-v2.db`（+ `-wal` / `-shm`） |
| 生产镜像 | `.env` 的 `GW_IMAGE=ai-gateway` + `GW_IMAGE_TAG=<版本>` |
| 入口域名 | 管理台 `gateway.5home.online`；数据面 `gatewayapi.5home.online`（宿主 Nginx 终结 TLS） |
| 当前版本 | `curl -sk https://127.0.0.1:17090/healthz` → `version`（旧格式**不含** `schema`） |
| 备用主机 | `tencent` = `124.223.188.186`（**P2 待就绪**,见 §6） |
| 主机是否有 `sqlite3` | **无**（备份走「停容器几秒」分支） |

> ⚠️ **升级前必做**：§3 快照。生产现在**默认没有自动备份**（`upgrade.sh` 尚未安装），
> **不带快照升级 = 出事无法回退到同一份库**。

---

## 1. 快照 / 备份（升级前强制）

生产主机无 `sqlite3` → `backup.sh` 走「**停容器 → 拷贝 → 起容器**」（数秒不可用），产物落
`/opt/ai-gateway-v2/data/backups/gateway-v2-<时间戳>.db`。

**方式 A（推荐,首次把它装上去,之后直接用）：**
```bash
ssh aliyun 'mkdir -p /opt/ai-gateway-v2'
scp deploy/scripts/backup.sh aliyun:/opt/ai-gateway-v2/
ssh aliyun 'chmod +x /opt/ai-gateway-v2/backup.sh'
ssh aliyun 'REMOTE_DIR=/opt/ai-gateway-v2 bash /opt/ai-gateway-v2/backup.sh'
```

**方式 B（不装,经 stdin 跑一次）：** 在仓库目录下执行
```bash
ssh aliyun 'REMOTE_DIR=/opt/ai-gateway-v2 bash -s' < deploy/scripts/backup.sh
```

**确认产物：**
```bash
ssh aliyun 'ls -lt /opt/ai-gateway-v2/data/backups/ | head'
```
> 记下这份快照的文件名（形如 `gateway-v2-20261008-153000-123456.db`），§5 回退要用。
> `KEEP` 默认 7 份,自动轮转。

---

## 2. 升级

> **当前链路**：ghcr 一键升级（`upgrade.sh`）**尚未启用** → 现用**离线链路** `deploy.sh`
> （本地构建镜像 → `docker save | ssh docker load` → 远端 compose up）。走 ghcr 后改用 `upgrade.sh`。

**升级前：先 §3 快照**，并记下当前版本（回退要用）：
```bash
ssh aliyun 'grep ^GW_IMAGE_TAG= /opt/ai-gateway-v2/.env'   # 记下旧版本,如 5dc947f
```

**离线升级（当前）：** 在本地仓库、checked-out 到要发布的提交（通常是 `dev`）后：
```bash
deploy/scripts/deploy.sh aliyun
```
它会：本地 build → 更新远端 `.env` 的 `GW_IMAGE_TAG=<新版本>` → 推送镜像 → 上传 compose/证书 → `compose up -d`。

**升级后确认：**
```bash
ssh aliyun 'docker ps --format "{{.Names}}\t{{.Image}}\t{{.Status}}" | grep gateway'
curl -sk https://gatewayapi.5home.online/healthz
```

> **将来（启用 ghcr 后）**：`ssh aliyun 'bash /opt/ai-gateway-v2/upgrade.sh <版本>'`（自动备份 + 健康校验 + 失败自动回滚），见 `deploy/README.md`。本手册届时同步更新。

---

## 3. 回退（升级失败时）

**要点：迁移单向**——新版本已把库前向迁移，**只换回旧镜像不够**，**必须连库一起还原**（换镜像 + 还原快照，两件都做）。

```bash
OLD=5dc947f                                  # ← 换成 §2 记下的旧版本
SNAP=gateway-v2-20261008-153000-123456.db    # ← 换成 §1 记下的快照文件名

# ① 停容器
ssh aliyun 'cd /opt/ai-gateway-v2 && docker compose stop gateway'

# ② 还原库(整份覆盖,连带清掉 WAL/SHM,避免旧 WAL 污染)
ssh aliyun "cp -p /opt/ai-gateway-v2/data/backups/$SNAP /opt/ai-gateway-v2/data/gateway-v2.db"
ssh aliyun 'rm -f /opt/ai-gateway-v2/data/gateway-v2.db-wal /opt/ai-gateway-v2/data/gateway-v2.db-shm'

# ③ 换回旧镜像
ssh aliyun "sed -i 's/^GW_IMAGE_TAG=.*/GW_IMAGE_TAG=$OLD/' /opt/ai-gateway-v2/.env"

# ④ 起容器
ssh aliyun 'cd /opt/ai-gateway-v2 && docker compose up -d'

# ⑤ 确认
ssh aliyun 'docker ps --format "{{.Names}}\t{{.Image}}" | grep gateway'
curl -sk https://gatewayapi.5home.online/healthz
```
> 旧镜像需仍在主机上（`docker images ai-gateway`）。若已 `prune` 掉，先经 `deploy.sh` 重建该版本再执行 ③④。

**退出症状对照**：容器反复重启（CrashLoop）多为「旧二进制读新库」→ 正是本节的场景；
迁移号不符、`sqlite` 报错同理。**先回退，再排查。**

---

## 4. 主机 / 进程故障

| 现象 | 处置 |
|---|---|
| 容器挂了但主机在 | `ssh aliyun 'cd /opt/ai-gateway-v2 && docker compose up -d'`（先看 `docker logs gateway` 定位） |
| 整机不可达（aliyun 宕） | **切到备用机 `tencent`** —— **当前尚未就绪（P2 待建,见 §6）** |
| 只想知道挂在哪 | `ssh aliyun 'docker compose ps; docker logs --tail=100 gateway'` |

> 若怀疑「升级引起」，直接走 §3 回退，比原地排查更快恢复。

---

## 5. AI 应急（网关挂了,Claude 也断了）

**问题**：本机 Claude Code 经 `~/.claude/settings.json` 指向网关
（`ANTHROPIC_BASE_URL=https://gatewayapi.5home.online`）。网关一挂,Claude 一起断 → 连排查
工具都没了。**用预置的备用端点一键复活：**

**一次性准备**（**现在就做,别等故障时**）：创建 `~/.claude/settings.fallback.json` ——
与 `settings.json` 同结构,但指向**不经本网关**的端点（官方 API,或第二台独立中转）。
示例见 `deploy/scripts/claude-fallback.sh` 末尾注释。

**故障时：**
```bash
deploy/scripts/claude-fallback.sh on     # 切到备用端点(自动备份当前网关配置)
# 然后**重启 Claude Code**(env 在启动时读取)
```
**网关恢复后：**
```bash
deploy/scripts/claude-fallback.sh off    # 切回网关
```
**随时查状态：**
```bash
deploy/scripts/claude-fallback.sh status
```

---

## 6. 切到备用机 `tencent`（P2,尚未就绪）

**目标拓扑**：`aliyun` 主、`tencent` 备（异云,真故障隔离）。备用机预置**同栈**
（compose + 证书 + **同 `GW_MASTER_KEY`** 的 `.env`）+ 定期接收**快照** + `failover.sh`（仓库尚缺,须新写）。

**就绪后的切换动作（草案,实现时细化）**：
1. `tencent` 拉最新快照 → 还原库 → `compose up -d`；
2. 把 `gatewayapi.5home.online` / `gateway.5home.online` 的**解析切到 `124.223.188.186`**（用户已确认可改 + 可设短 TTL/健康检查）；
3. 验证 `curl -sk https://gatewayapi.5home.online/healthz`；
4. 恢复后切回 `aliyun`,并**对账**故障期落在 `tencent` 的写入（对齐 `HA.md §5`）。

> **当前不可用**：`tencent` 仅装好 docker/工具,**未预置备机栈**。此节为 P2 交付后的操作说明。落地见 `deploy/HA.md` §8。

---

## 7. 事件后核对清单

- [ ] `/healthz` 的 `version` 与预期一致；
- [ ] 管理台能登录、数据面 `/v1/models` 能列；
- [ ] 若回退过：确认库回到正确快照（抽查一条近期记录）；
- [ ] 若切过机：对账故障窗口内的写入；
- [ ] 飞书告警是否触发（`deploy/scripts/notify.sh`，需设 `FEISHU_WEBHOOK`；机器人开加签时另设 `FEISHU_SECRET`）；
- [ ] 复盘：把本次「升级 → 失败 → 回退」写回本手册 / `HA.md`。
