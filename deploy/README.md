# deploy/ — 生产部署

生产主机 = 云主机 **`aliyun`**(`47.116.65.140`,`ssh aliyun`,root),目录 **`/opt/ai-gateway-v2`**。
> 注:`lab`(192.168.0.202)已**不是**生产 —— 旧文档写的「lab 轮询自更新」在现网从未启用
> (无 `gw-updater` 定时器)。现网入口只有两个域名(2026-09-21 上线),见下。

## 链路总览

```
push main / 打 tag v*  ──►  GitHub Actions (.github/workflows/release.yml, 云端)
                              ├─ npm ci && npm run build        (前端)
                              ├─ GOOS=linux GOARCH=amd64 go build
                              ├─ docker build (纯组装, ~46MB;烘焙 APP_VERSION/GIT_SHORT/SCHEMA_HEAD)
                              └─ push ghcr.io/glacierzzz26/personal-ai-gateway:<ver>
                                       └─ main 另推 :latest;打 v* tag 另推 :<tag>
                                                  │
                                                  ▼
aliyun: /opt/ai-gateway-v2  ──  docker compose up -d   (镜像自 ghcr 匿名拉取)
                              └─ 升级:bash /opt/ai-gateway-v2/upgrade.sh <版本>   ← 一键升级/回滚
```

- **版本号口径单一来源**:`deploy/scripts/lib-version.sh` 的 `gw_version` —— HEAD 正好在 `v*` tag 上为
  `v<tag>-<sha7>`,否则恒为 **`v0.0.0-<sha7>`**(未发版不给版本号);工作区脏加 `-dirty`。
  口径同时被 `release.yml` / `build.sh` / `deploy.sh` 复用,不再各算一份。
- **光有短 hash 不够**:镜像内烘焙 `APP_VERSION`(版本号)与 `GIT_SHORT`(短 hash)两项,
  `/healthz` 回 `version`(如 `v0.0.0-4de1cde`)+ `schema`(本库已应用的最大迁移号)。
- **部署路径上没有编译**:aliyun 只拉镜像 + 重建。

## 一键升级 / 回滚:`upgrade.sh`

装在 aliyun `/opt/ai-gateway-v2/upgrade.sh`(依赖只用宿主已有的 **docker + compose v2 + curl**,
不依赖 jq/python/sqlite3)。非交互,可 `ssh` 直调。

```bash
ssh aliyun 'bash /opt/ai-gateway-v2/upgrade.sh --list'            # 列出 ghcr 可用版本(标 current/latest)
ssh aliyun 'bash /opt/ai-gateway-v2/upgrade.sh v0.0.0-4de1cde'    # 升/切到指定版本
ssh aliyun 'bash /opt/ai-gateway-v2/upgrade.sh --latest'          # 升到 ghcr :latest
ssh aliyun 'bash /opt/ai-gateway-v2/upgrade.sh --rollback'        # 撤销上次升级(换镜像 + 还原库快照)
```

| 参数 | 含义 |
|---|---|
| `<版本>` | 升/切到指定版本(如 `v0.0.0-4de1cde`) |
| `--latest` | 升到 ghcr `:latest` |
| `--list` | 列出 ghcr tag(标记 `current` / `latest`;**不声称先后** —— 非发版构建同前缀) |
| `--rollback` | 撤销上次升级:换回镜像**并还原升级前的库快照** |
| `--force` | 越过降级护栏(位置随意;跨迁移且无配套快照仍拒绝) |

**升级流程**:解析目标 → 幂等(镜像 ID 相同即「已是该版本」)→ 并发锁 → **强制备份** →
记录 prev(含库快照路径)→ 拉镜像 → **降级护栏** → 切 `.env` → 重建 → **双重健康校验**
(容器 image ID == 目标 **且** `/healthz` version == 目标)→ 失败自动回滚(同 schema)或交人工(跨 schema)。

**降级护栏**(迁移单向的硬约束):
- 目标 schema head **<** 当前库已应用号 → **拒绝**(迁移单向,旧二进制读不了新库),`--force` 可越过。
- 目标镜像**无 schema 标识**(旧格式)→ 无法证明它能读此库,同样拒绝,需 `--force`。
- **例外:`--rollback`** 若带配套库快照(升级前刚备的那份),**跳过护栏** —— 快照与目标版本同代,
  还原后不存在「旧二进制读新库」。这是升级失败后唯一正确的救命路径。

**退出码**:

| 码 | 含义 | 码 | 含义 |
|---|---|---|---|
| 0 | 成功 / 幂等 | 5 | 目标版本拉取失败 |
| 1 | 前置条件不满足 | 6 | 降级被拒(需 `--force`) |
| 2 | 用法错误 | 7 | 校验失败但已自动回滚 |
| 3 | 并发锁被占 | 8 | 校验失败且未回滚(跨 schema,需人工 `--rollback`) |
| 4 | 备份失败 | 9 | `--rollback` 无状态记录 |

> ⚠️ **升级会短暂停机**:aliyun 无 `sqlite3`,`backup.sh` 走「停容器 → 复制 → 起容器」分支(数秒)。
> 备份是升级的**强制前置**,失败即中止(绝不带着未备份的库升级)。

## 域名边缘(Nginx + 公信证书)

域名上线后,生产主机上多一层**宿主原生 Nginx**(非容器),用公信证书终结 TLS,
**客户端不再需要导入自签 CA**。**两面各占一个子域、都在 443**(按 SNI 主机名分流,不是按端口):

> ⚠️ **边缘不归本仓库**。Nginx 是**宿主级、多服务共用**的资源(443 要承载很多子域/服务),
> 配置与证书集中放在**独立仓库 `host-infra`**;本仓库只声明"网关占哪些端口、回源到哪"。
> 边缘的模板、上线脚本、证书流程与回滚全在那边,这里只登记**网关侧需要配合的点**。

```bash
# 边缘上线(在 host-infra 仓库里)
cd ../host-infra && sudo DOMAIN=5home.online bash scripts/deploy.sh
```

| 入口 | 端口 | 域名 | 证书 | 回源 |
|---|---|---|---|---|
| 管理台/登录 | 443 | `gateway.5home.online` | 公信(`*.5home.online`) | `https://127.0.0.1:17090` |
| 数据面(API) | 443 | `gatewayapi.5home.online` | 公信(`*.5home.online`) | `https://127.0.0.1:17081` |
| 其它(裸 IP / 未知主机名) | 443 | — | — | **444,直接断连** |

- **只有这两个域名能进来**:Nginx **不监听 17080/17090**,容器发布口一律只绑 `127.0.0.1` ——
  「裸 IP + 非标端口」时代的入口(`:17080` 过渡口、公网 `:17090`)**已随域名稳定下线**,公网无处可绕。
- **网关侧必须配合的点**:
  1. `deploy/docker-compose.yml` 两个口都收窄为 `127.0.0.1`——数据面 `127.0.0.1:17081:17080`(公网口与回源口
     必须错开,同号会撞 bind)、管理台 `127.0.0.1:17090:17090`;
  2. 自签证书 `deploy/certs/` 保留 —— Nginx 回源用它(**仅此一处**);
  3. 管理台「系统设置 → 对外基址」填 `https://gatewayapi.5home.online`。
- **回源仍用 https**:网关自签 TLS 同时承担数据面/管理台两面物理隔离;Nginx **覆写**
  `X-Forwarded-Proto`(网关无条件信任该头,决定 Secure Cookie 与基址推断)。
- **回滚旧入口**(要恢复裸 IP / 非标端口):compose 端口绑定改回 `17080:17080` / `17090:17090`
  → `docker compose up -d`,同时在 host-infra 的 vhost 里补回 `listen 17080` 块并重新渲染。
  两块都要动,只改一边会出现「端口开着但 Nginx 没接」或「bind 冲突」。

## 一次性初始化(启用 ghcr 一键升级的前置)

以下三步完成前,`upgrade.sh` 装得上但**拉不到 ghcr 镜像**;当前生产仍在跑旧格式本地镜像。

### 1. 让 `release.yml` 进入 `main`(人工,走发布纪律)

`.github/workflows/release.yml` 现只存在于 `dev` 分支。GitHub 只认默认分支(`main`)上的 workflow,
故需按发布纪律把 `dev → main` 同步一次,workflow 才会生效。**这是纯人工动作,AI 不碰。**

### 2. ghcr 包设为 public(首次 Actions 跑通后)

push 到 main(或手动 `workflow_dispatch`)后,去
GitHub → 你的头像 → Packages → `personal-ai-gateway` → Package settings → Change visibility → **Public**。
设成 public 后 aliyun **匿名拉取**,无需任何凭证(源码本就 public)。
未转 public 时匿名 token 会失败,`upgrade.sh --list` 会明确报错(不静默)。

### 3. 改 aliyun 的 `.env`(去掉本地短名)

现网 `.env` 里有 `GW_IMAGE=ai-gateway`(本地短名,来自旧的 `save|ssh|load` 链路)。
启用 ghcr 后**删掉这一行**,让 compose 走缺省的 ghcr 全名。

```bash
ssh aliyun 'sed -i "/^GW_IMAGE=/d" /opt/ai-gateway-v2/.env'
```

### 4. 在 aliyun 装脚本(首次)

```bash
ssh aliyun 'mkdir -p /opt/ai-gateway-v2'
scp deploy/scripts/upgrade.sh deploy/scripts/backup.sh deploy/scripts/lib-version.sh \
    aliyun:/opt/ai-gateway-v2/
ssh aliyun 'chmod +x /opt/ai-gateway-v2/{upgrade,backup}.sh'
```

`upgrade.sh` 会调用同目录的 `backup.sh`(强制备份),两者必须一起装。

> **关于轮询自更新**:仓库里仍留有 `deploy/scripts/update-from-ghcr.sh` + `deploy/gw-updater.{service,timer}`
> (2026-09-15 的旧方案),但**现网从未安装**。启用它会让定时器把版本自动顶到 `:latest`,
> 与「按指定版本升级 / 钉版本」冲突 —— 除非明确要回到全自动模式,否则**不要装**。日常升级走 `upgrade.sh`。

## 日常流程

- **发新版**:合进 `main`(或打 `v*` tag)→ Actions 编译推 ghcr。
- **升级生产**:`ssh aliyun 'bash /opt/ai-gateway-v2/upgrade.sh <版本>'`(或 `--latest`)。
- **确认版本**:`curl -s https://gatewayapi.5home.online/healthz` 回显 `version` 与 `schema`。
- **失败**:脚本同 schema 会自动回滚(退出 7);跨 schema 时**不自动回滚**(退出 8),
  确认后用 `upgrade.sh --rollback` 连库一起退回。

## 回滚

1. **常规**:`ssh aliyun 'bash /opt/ai-gateway-v2/upgrade.sh --rollback'` —— 换回上次升级前的镜像
   **并把库还原到升级前的快照**(迁移单向,只换镜像不够)。状态记在 `/opt/ai-gateway-v2/.upgrade-state`。
2. **应急/离线兜底**:`deploy/scripts/deploy.sh` 仍是完整的 `build → save | ssh load → compose up` 通道,
   不经 ghcr,适合 ghcr 挂掉时。

## 本地/应急链路:`deploy.sh`

```bash
deploy/scripts/deploy.sh [GW_HOST]        # 默认 aliyun
```

它本地构建 `ai-gateway:<ver>`、`docker save | ssh docker load` 推到目标机,远端 `GW_IMAGE=ai-gateway docker compose up -d`。
与 ghcr 链路靠 `GW_IMAGE` 区分(缺省 ghcr 全名,deploy.sh 覆盖为本地短名),`.env` 的
`GW_MASTER_KEY` 两条链路共用、不互相覆盖。**版本号同样出自 `lib-version.sh`**。

> `deploy/scripts/local-deploy.sh`(为「带 updater 时本地部署」而写)在现网**无用武之地**
> (未装 updater),保留作历史。

## 风险 / 注意

- **首拉延迟**:aliyun 首次拉 ghcr 全量约 46MB;之后仅传变更层,秒级。
- **升级停机**:无 `sqlite3` → 备份走停机分支,每次升级数秒不可用。要零停机需装 `sqlite3`(宿主 apt,不在本次范围)。
- **备份保留**:`data/backups/` 只留最近 `KEEP`(缺省 7)份;`--rollback` 依赖升级前那份,别把 KEEP 调到 0。
- **基镜像**:`Dockerfile.gateway` 按 digest 固定 alpine 且靠本地缓存/`docker save`,变更基镜像需在
  构建机 `docker pull` 后重取 digest 再改 Dockerfile。
