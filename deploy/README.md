# deploy/ — 生产部署

## 链路总览

```
push main / 打 tag v*  ──►  GitHub Actions (.github/workflows/release.yml, 云端)
                              ├─ npm ci && npm run build        (前端)
                              ├─ GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=<sha>"
                              ├─ docker build (纯组装, ~46MB)
                              └─ push ghcr.io/glacierzzz26/personal-ai-gateway:<sha> 与 :latest
                                          │
                                          ▼  (ghcr.io —— lab 唯一能到达的外网)
lab: gw-updater.timer (每 60s, systemd user)
     └─ update-from-ghcr.sh ──┬─ :latest digest 变了 → GW_IMAGE_TAG=<tag> && compose up -d  (~8s)
                              └─ digest 没变       → 无操作
```

约束来自实测(见 `CI.md` §1):lab 连不通 github.com、连不通 Docker Hub,但**能拉 ghcr.io**;
lab 在私网 NAT 后,云端 runner **无法主动连入** → 只能 lab 侧轮询。设计细节在 `CI.md`。

## 域名边缘(Nginx + 公信证书)

域名 `5home.online` 上线后,生产主机上多一层**宿主原生 Nginx**(非容器),用公信证书终结 TLS,
**客户端不再需要导入自签 CA**。

> ⚠️ **边缘不归本仓库**。Nginx 是**宿主级、多服务共用**的资源(443 要承载很多子域/服务),
> 配置与证书集中放在**独立仓库 `host-infra`**;本仓库只声明"网关占哪些端口、回源到哪"。
> 边缘的模板、上线脚本、证书流程与回滚全在那边,这里只登记**网关侧需要配合的点**。

```bash
# 边缘上线(在 host-infra 仓库里)
cd ../host-infra && sudo DOMAIN=5home.online bash scripts/deploy.sh
```

| 入口 | 端口 | 证书 | 回源 |
|---|---|---|---|
| 管理台/登录 | 443 | 公信(`*.5home.online`) | `https://127.0.0.1:17090` |
| 数据面 | 17080 | SNI=域名→公信;裸 IP(无 SNI)→自签回退 | `https://127.0.0.1:17080` |
| 旧 IP 管理台 | 17090 | 自签(网关自持) | —(Nginx 不碰) |

- **旧 IP 客户端为什么不断**:裸 IP 握手无 SNI,Nginx 落到 `:17080 default_server` 自签回退块 →
  已导入 CA 的老客户端照常验真;域名客户端带 SNI 取公信证书。**双入口并存**,回滚只需把
  `deploy/docker-compose.yml` 的端口绑定改回 `17080:17080`。
- **网关侧必须配合的点**:
  1. `deploy/docker-compose.yml` 把数据面发布收窄为 `127.0.0.1:17080:17080`(公网 17080 归 Nginx);
  2. 自签证书 `deploy/certs/` 保留 —— Nginx 回源用它,裸 IP:17080 的 SNI 回退也用它,
     故 **`gen-certs.sh` 的 `GW_PUBLIC_IP` SAN 不可去**(去掉则旧 IP 客户端验真失败);
  3. 管理台「系统设置 → 对外基址」填 `https://5home.online:17080`。
- **回源仍用 https**:网关自签 TLS 同时承担数据面/管理台两面物理隔离;Nginx **覆写**
  `X-Forwarded-Proto`(网关无条件信任该头,决定 Secure Cookie 与基址推断)。

## 一次性初始化

### 1. ghcr 包设为 public(首次 Actions 跑通后)

push 到 main(或手动 `workflow_dispatch`)后,去
GitHub → 你的头像 → Packages → `personal-ai-gateway` → Package settings → Change visibility → **Public**。
设成 public 后 lab **匿名拉取**,无需任何凭证(源码本就 public)。

### 2. lab 上装 updater(systemd **user** 单元,免 sudo)

```bash
scp deploy/scripts/update-from-ghcr.sh deploy/gw-updater.service deploy/gw-updater.timer \
    rguo@192.168.0.202:~/ai-gateway/
ssh rguo@192.168.0.202 'mkdir -p ~/.config/systemd/user && \
  cp ~/ai-gateway/gw-updater.service ~/ai-gateway/gw-updater.timer ~/.config/systemd/user/ && \
  systemctl --user daemon-reload && \
  systemctl --user enable --now gw-updater.timer && \
  loginctl enable-linger rguo'
```

`enable-linger` 让 lab 未登录时 timer 也照跑。查看:
`ssh rguo@192.168.0.202 'systemctl --user list-timers gw-updater; journalctl --user -u gw-updater -n 50'`

### 3. lab 的 compose 走 ghcr(已默认,无需改文件)

`docker-compose.yml` 的 `image` 用 `${GW_IMAGE:-ghcr.io/glacierzzz26/personal-ai-gateway}:${GW_IMAGE_TAG:-latest}`
——缺省即 ghcr 全名。updater 只改 `.env` 里的 `GW_IMAGE_TAG`,不碰 compose 文件。

## 日常流程

- **发新版**:合进 `main`(或打 `v*` tag)→ Actions 编译推 ghcr → lab 在 ~60s 内自更新。
- **确认版本**:管理台/数据面 `GET /healthz` 回显 `<sha>`;或 `ssh lab 'docker ps --format "{{.Image}}" | grep gateway'`。
- **手动触发一次更新**(不等 60s):`ssh rguo@192.168.0.202 'systemctl --user start gw-updater.service'`。
- **手动跑脚本**(带输出):`ssh rguo@192.168.0.202 'bash ~/ai-gateway/update-from-ghcr.sh'`。

## 回滚

1. **ghcr 链路**:lab 上 `sed -i 's|^GW_IMAGE_TAG=.*|GW_IMAGE_TAG=<旧sha>|' ~/ai-gateway/.env && cd ~/ai-gateway && docker compose up -d`。
   旧版本镜像按 sha 打了 tag,仍在 ghcr,可直接拉。注意 updater 下轮会把 `.env` 改回 `:latest`
   对应的新版本 —— 要长期钉旧版,先 `systemctl --user stop gw-updater.timer`,或直接用上面的
   `local-deploy.sh KEEP_STOPPED=1`。
2. **本地链路兜底**:`deploy/scripts/deploy.sh` 仍是完整的 `build → save | ssh load → compose up` 通道,
   显式 `GW_IMAGE=ai-gateway`(本地短名),不经 ghcr,适合 ghcr 挂掉或需要临时改配置时。

## 本地/应急链路:`deploy.sh`

```bash
deploy/scripts/deploy.sh [GW_HOST]        # 默认 rguo@192.168.0.202
```

它本地构建 `ai-gateway:<ver>`、`docker save | ssh docker load` 推到目标机,远端 `GW_IMAGE=ai-gateway docker compose up -d`。
与 ghcr 链路并存:镜像名靠 `GW_IMAGE` 区分(缺省 ghcr,deploy.sh 覆盖为本地短名),`.env` 的
`GW_MASTER_KEY` 两条链路共用、不互相覆盖。

### 带 updater 时的本地升级:`local-deploy.sh`

装了 `gw-updater.timer` 后直接用 `deploy.sh` 会被它每 60s 顶回 ghcr 版本。用这个包一层:

```bash
deploy/scripts/local-deploy.sh [GW_HOST]   # 默认 rguo@192.168.0.202
```

它依次:**停 timer → 等正在跑的那轮 updater 收尾 → `deploy.sh` 本地构建部署 → 对齐对账基线 → 恢复 timer**。
关键在第 4 步:把 `.deployed-digest` 对齐到 ghcr 当前 `:latest`,于是 timer 恢复后**不会**立刻把你顶回去,
而是让本地版本一直跑到 ghcr 出下一个新版本为止。

| 变量 | 作用 |
|---|---|
| `KEEP_STOPPED=1` | 部署后不恢复 timer(长时间钉本地版);恢复:`ssh <host> 'systemctl --user start gw-updater.timer'` |
| `NO_ALIGN=1` | 跳过对账基线对齐(下次轮询若 ghcr 有更新会切回 ghcr) |
| `REMOTE_DIR=…` | 覆盖远端目录 |

lab 上没装 updater 时,脚本自动跳过停/恢复,等同于直接 `deploy.sh`。任何情况下失败都不会破坏当前运行的版本(部署前若发现有在途的 updater 运行,直接放弃本次部署)。


## 风险 / 注意

- **首拉延迟**:lab 首次拉 ghcr 全量约 46MB;之后仅传变更层,秒级。
- **轮询间隔**:60s → 推送后最多 ~60s 生效。要更快改 `gw-updater.timer` 的 `OnUnitActiveSec`。
- **并发**:updater 用 `flock /tmp/gw-updater.lock` 自我互斥;与手动 `deploy.sh` 同时跑仍可能撞 compose,
  手动操作前最好先 `stop` timer。
- **基镜像**:`Dockerfile.gateway` 按 digest 固定 alpine 且靠本地缓存/`docker save`,变更基镜像需在
  构建机 `docker pull` 后重取 digest 再改 Dockerfile(lab 不通 Docker Hub)。
