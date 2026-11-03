# 生产拓扑图

AI Gateway 双节点高可用架构的静态拓扑图，包含节点、容器、端口与数据流向。
纯静态、无外部依赖、无 JavaScript，可直接双击打开或托管到任意静态站点。

## 文件

| 文件 | 说明 |
|---|---|
| `index.html` | 展示页：标题、拓扑图、图例、节点明细卡片、页脚 |
| `topology.svg` | 拓扑图本体，手写内联 SVG，固定画布 1520 × 1280 |

`index.html` 通过 `<img src="topology.svg">` 引用图片，**`topology.svg` 是唯一图源**，改图只改这一个文件。

## 查看

直接双击 `index.html` 即可（`file://` 可用，无需构建、无需联网）。

如需通过 HTTP 查看：

```bash
cd deploy/topology
python3 -m http.server 8099
# 浏览器打开 http://127.0.0.1:8099/
```

打印 / 导出 PDF：页面已内置打印样式，浏览器「打印 → 另存为 PDF」即可。

## 数据来源

图中信息来自 2026-11-03 对 tencent / aliyun 两台生产机的实际巡检：

```bash
# 容器与镜像
docker ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'

# 定时任务与 systemd 单元
systemctl list-timers --all
systemctl cat ha-controller.timer gw-pg-tunnel.service

# nginx 站点与反代目标
nginx -T 2>/dev/null | grep -E 'server_name|listen|proxy_pass'

# 数据库主从与复制状态
docker exec ai-gateway-v2-db-1 pg_isready
docker exec ai-gateway-v2-db-1 psql -U postgres -c 'SELECT client_addr, state, sync_state FROM pg_stat_replication;'

# 备份计划
crontab -l | grep pg_dump
```

## 更新拓扑

`topology.svg` 为**手工排版**，节点位置、连线、文字均为显式坐标，修改时：

1. 按上面的命令重新核对节点 / 容器 / 端口；
2. 直接编辑 `deploy/topology/topology.svg`；
3. 同步更新 `index.html` 中的明细卡片与「数据核对」日期；
4. 本地渲染确认无遮挡（见下）。

### 本地渲染校验（可选）

SVG 里所有面板都是白色卡片，跨面板连线必须**放在文件末尾**绘制，否则会被卡片填充盖住。
改完连线顺序后建议栅格化看一眼：

```bash
pip install --break-system-packages --target /tmp/pylibs cairosvg
PYTHONPATH=/tmp/pylibs python3 -c \
  "import cairosvg; cairosvg.svg2png(url='deploy/topology/topology.svg', write_to='/tmp/topology.png', output_width=1520, output_height=1280)"
```

（系统 Python 受 PEP 668 管理，故用 `--target` 装到临时目录；也可用任意 SVG 查看器直接打开。）

### 为什么不用 D2 自动布局

曾用 D2（`tala` / `elk` / `dagre` 三种布局）尝试自动生成，但在当前节点数量下
均出现连线交叉、标签重叠或画布比例失衡（如 8868 × 4436 的极端宽幅），观感不可用。
因此最终改为手写 SVG，换取布局与视觉的完全控制。**不要再把 D2 作为生成入口**。

## 安全说明

图中只画结构与端口，**不包含**任何密钥、口令、token、webhook 地址或数据库连接串。
新增节点时同样不要把这些信息写进图里。
