# CI/CD 方案:Actions 打包 → ghcr → lab 轮询自更新

> 状态:**已实施**(2026-09-15)。方案定稿于 2026-09-14,当日落地:见 `.github/workflows/release.yml`、
> `deploy/scripts/update-from-ghcr.sh`、`deploy/gw-updater.{service,timer}`、`deploy/README.md`。
> 一次性初始化(ghcr 包转 public、lab 装 timer)见 `README.md`。本文保留为方案与决策记录。
> 目标:push/打 tag 后**快速**把新版本送上生产。编译慢无所谓 —— 云端 runner 编译,
> lab 只做「拉镜像 + 重建」,部署路径上没有编译。

## 1. 约束(实测,决定了方案形状)

| 事实 | 实测值 | 对方案的影响 |
|---|---|---|
| 仓库可见性 | **PUBLIC** | ghcr 包若设 public,lab 可**匿名拉取**,无需任何凭证 |
| lab 能否拉 ghcr.io | ✅ 能(层正常下载) | **ghcr 是唯一可用传输通道** |
| lab 能否连 github.com | ❌ 超时(15s `000`) | 不能注册 self-hosted runner |
| lab 能否连 Docker Hub | ❌ 不通 | 不能 `docker pull` 官方基础镜像(故镜像基底 alpine 靠本地缓存/`docker save`) |
| lab 网络位置 | 私网 NAT 后 | 云端 runner **无法**主动连入 lab |
| frpc 暴露面 | 仅转发 17080 / 17090(**未暴露 22**) | 排除了「云端跳板 SSH 进 lab」的即时触发 |
| 公网入口 | **域名(2026-09-21 上线)**:宿主 Nginx 终结公信证书,`gateway.5home.online`(管理台)/ `gatewayapi.5home.online`(数据面),均 443,回源网关 127.0.0.1 | 见 `deploy/README.md`「域名边缘」;`:17080` 过渡口与旧 IP 入口过渡期并存 |
| 镜像体积 | **45.7MB**(alpine 基底已缓存) | 每次部署实际只传变更层,很快 |
| 生产主机 | `lab` = 192.168.0.202(ssh 免密,sudo 需密码) | lab 端组件用 **systemd user 单元**(免 sudo) |

**结论**:传输通道只能是 ghcr;lab 无法被主动连入 → lab 侧必须**轮询**。

## 2. 架构

```
push main 或 打 tag v*   ──►  GitHub Actions (ubuntu-latest, 云端)
                                ├─ npm ci && npm run build        (前端)
                                ├─ GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=<sha>"
                                ├─ docker build (纯组装, 45MB)
                                └─ push ghcr.io/glacierzzz26/personal-ai-gateway:<sha> 与 :latest
                                            │
                                            ▼  (ghcr.io —— lab 唯一能到达的外网)
lab: gw-updater 定时器(每 60s)
     └─ docker pull :latest ──┬─ digest 变了 → 改 .env GW_IMAGE_TAG=<sha> && docker compose up -d  (~8s)
                              └─ digest 没变 → 无操作
```

- GitHub 侧只负责**编译打包上传**(你的原话:「编译无所谓」)。
- lab 侧只负责**拉取 + 重建**,永远不编译。
- `version` 注入 `<sha>`,`/healthz` 与现有 `git describe --always` 口径一致,便于核对。

## 3. 已定决策

| 决策点 | 选择 |
|---|---|
| Actions 触发点 | **push main 或 打 tag `v*`** 都触发。tag 额外打 `:<tag>`,`:latest` 始终跟 main |
| lab 如何得知新版本 | **lab 轮询 ghcr `:latest` 的 digest**,变了才重建(无操作时零成本,无新增公网入口) |
| ghcr 包可见性 | **public,免登录**(lab 直接拉;源码本就 public) |
| 与本地 `deploy.sh` 关系 | **两条并存**。deploy.sh 的 `save\|ssh\|load` 保留为应急/回滚通道;ghcr 链路是常规路径 |
| auto-fix | **删除**(workflow + `.claude/autofix.toml` 及其所需 secrets/vars) |

## 4. 实施清单(待做)

### 新增
- `.github/workflows/release.yml`
  - `on: { push: { branches: [main], tags: ['v*'] }, workflow_dispatch: {} }`
  - `permissions: { contents: read, packages: write }`(用内置 `GITHUB_TOKEN` 推 ghcr,**无需新建 PAT**)
  - 步骤:checkout → setup-node → setup-go → 前端构建 → Go 交叉编译 → `docker build` → `docker login ghcr.io` → 打 tag(`:<sha>` + `:latest`,`v*` 再加 `:<tag>`)→ push
  - Action 版本 pin(SHA 或 tag),不引第三方未 pin 的 action
- `deploy/scripts/update-from-ghcr.sh`(lab 侧)
  - `docker pull <image>:latest` → 取新 digest → 与当前运行容器镜像 digest 比对 → 变了才 `GW_IMAGE_TAG=<sha> docker compose up -d`;没变直接退出
  - 幂等,可重复执行;失败不破坏当前运行版本
- `deploy/gw-updater.service` + `deploy/gw-updater.timer`(systemd **user** 单元)
  - `OnUnitActiveSec=60s`;配 `loginctl enable-linger rguo` 让未登录也在跑
- `deploy/README.md`:一次性初始化步骤(见 §5)

### 修改
- `deploy/scripts/build.sh`:支持「只后端 / 只前端」或直接产出给 Actions 复用(不重写,轻改参数化)

### 删除
- `.github/workflows/auto-fix.yml`
- `.claude/autofix.toml`

## 5. 一次性初始化(需你手动执行)

1. **首次 Actions 跑通后**,去 GitHub → Packages → 选该包 → 设为 **public**(Settings → Change visibility)。
2. **lab 上安装 updater**:
   ```bash
   scp deploy/scripts/update-from-ghcr.sh deploy/gw-updater.* rguo@192.168.0.202:~/ai-gateway/
   ssh rguo@192.168.0.202 'mkdir -p ~/.config/systemd/user && \
     cp ~/ai-gateway/gw-updater.{service,timer} ~/.config/systemd/user/ && \
     systemctl --user daemon-reload && systemctl --user enable --now gw-updater.timer && \
     loginctl enable-linger rguo'
   ```
3. **lab 的 compose 改用 ghcr 镜像**:把 `deploy/docker-compose.yml` 的 `image:` 改为 ghcr 全名(或让 updater 脚本用 `GW_IMAGE_TAG` + 环境覆盖)。**注意**:当前 compose 的 `image: ai-gateway:${GW_IMAGE_TAG}` 是本地短名,新链路要用 `ghcr.io/glacierzzz26/personal-ai-gateway:${GW_IMAGE_TAG}`。**此项与「deploy.sh 两条并存」有冲突需在实施时解掉**(见 §6)。

## 6. 待解冲突与风险

- **[需实施时解决] compose image 名二选一**:ghcr 链路要 `ghcr.io/...` 全名,deploy.sh 要本地短名。两条并存时,建议 compose 用 `GW_IMAGE` 变量(缺省 ghcr 全名),deploy.sh 显式覆盖为本地短名。
- **首拉延迟**:lab 首次拉 ghcr 全量约 45MB;之后仅变更层,秒级。
- **轮询间隔 vs 部署延迟**:60s 轮询 → 推送后最多 ~60s 生效。要更快可缩短(代价:glab 上多打几次 ghcr,几乎可忽略)。
- **并发**:updater 与手动 deploy.sh 同时触发可能撞 compose;updater 脚本应加 `flock`。
- **回滚**:`GW_IMAGE_TAG` 指回旧 sha 即可(ghcr 保留历史 tag);本地 `ai-gateway:<oldsha>` 镜像也还在,双保险。
- **secrets 清理**:删 auto-fix 后,仓库里 `AUTOFIX_*` 的 vars/secrets 可一并删除(需你在 GitHub 设置里操作)。
