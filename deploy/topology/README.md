# 全域机器拓扑（静态快照）

七台主机的静态拓扑图，覆盖每个节点上的容器 / 服务、端口与关键数据流。
面向「一眼看清整片基础设施」，**不是实时监控**——数据为 2026-11-03 的人工核对快照。

## 文件

| 文件 | 说明 |
|---|---|
| `topology.svg` | 手绘 SVG 拓扑图（1800×1560），唯一图形真源，无外部依赖 |
| `index.html` | 浅色主题展示页：内嵌 SVG + 图例 + 全部主机/上游/外部服务明细卡片 |

## 查看

```bash
cd deploy/topology
python3 -m http.server 8099
# 浏览器打开 http://127.0.0.1:8099/
```

也可直接打开 `topology.svg`（浏览器原生渲染）。`index.html` 只通过相对路径引用 `topology.svg`，
无 CDN、无外部字体、无脚本，离线可用。

## 主机清单

| 主机 | 地址 | 角色 | 关键服务 |
|---|---|---|---|
| tencent | 124.223.188.186 | 生产主节点 | nginx `:443`；gateway 数据面 `:17080` / 管理台 `:17090`；PG16 主库 `:5433`；nexus `:8080`；每日 `pg_dump` |
| aliyun | 47.116.65.140 | 灾备节点 | nginx `:443`；PG16 从库；`gw-pg-tunnel :15432`；`ha-controller.timer`（60s）；frps `:17000` + visitors；litesentry `:17002` |
| vps | 162.35.36.104 | 海外代理出口 | sing-box VLESS-REALITY `:443/TCP`、Hysteria2 `:443/UDP`、VLESS-WS `:8081`；Caddy `:8080`；cloudflared 副本 A；fail2ban |
| la | 154.81.37.25 | 海外代理出口 | sing-box Hysteria2 `:443/UDP`、VLESS-WS `:8081`；Caddy `:8080`；cloudflared 副本 B（同一隧道）；la-ssh |
| 本地 WSL | 192.168.0.100 | 开发机 | Docker piksp4 栈；`piks-postgres`、`quant-test-pg :55432`；frpc → aliyun frps；SSH `:17001` / 预览 `:17099` |
| quant-server | 192.168.0.201 | 家庭内网 | invest-web（frp stcp）→ aliyun `:17011` |
| lab | 192.168.0.202 | 家庭内网 | piks-web（frp stcp）→ aliyun `:17010`（当前离线） |

上游：DeepSeek 官方（`api.deepseek.com`）、OpenCode Zen（`opencode.ai/zen/go/v1`），均经网关数据面转发。
外部依赖：飞书机器人（告警/日报）、DNSPod（`5home.online` 解析）、Cloudflare（`rguo.top` Tunnel）、GitHub / ghcr（镜像交付）。

## 数据来源

图上的端口、容器名与地址来自对各主机的实际核对，常用只读命令：

```bash
# 容器与端口
docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Ports}}'
# 宿主监听端口
ss -lntup
# systemd 定时器
systemctl list-timers --all
# frp 配置
cat /etc/frp/frpc.toml /etc/frp/frps.toml 2>/dev/null
```

> `192.168.0.201` / `192.168.0.202` 两台家庭内网主机**未直接登录核对**，其角色（invest-web / piks-web）
> 由 aliyun 上 frps 的 stcp visitor 配置与域名回源关系推断。

## 更新流程

1. 按上面的命令重新核对端口 / 容器 / 主机角色。
2. 改 `topology.svg`（坐标手写，改动后局部渲染确认无重叠）。
3. 同步更新 `index.html` 的明细卡片与本文清单。
4. 更新图内与页脚的「数据核对」日期。

本地把 SVG 转 PNG 预览（可选，需 `cairosvg`）：

```bash
python3 -c "import cairosvg; cairosvg.svg2png(url='topology.svg', write_to='/tmp/topology.png', output_width=1800)"
```

若系统 Python 受 PEP 668 管理，用 `pip install --target /tmp/pylibs cairosvg` 后以
`PYTHONPATH=/tmp/pylibs` 运行。

## 为什么是手绘 SVG

试过 D2 / Graphviz 等自动布局，节点一多连线就互相穿插、标签重叠，且难以做出统一的浅色视觉语言。
本图固定 `viewBox 0 0 1800 1560`、逐元素定位，换来可预测的排版；代价是改结构时要手动挪坐标。

## 安全

图与页面**不含任何密钥**：frp auth token、frps dashboard 凭据、litesentry 初始密码、Cloudflare TUNNEL_TOKEN
均未出现。仅列主机地址、端口、容器名与域名。
