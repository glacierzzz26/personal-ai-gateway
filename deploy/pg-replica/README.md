# PG 主从流复制（双云 HA）

> 本目录是把 `aliyun` 作为 **`tencent` 的 PostgreSQL 流复制热备**（standby，只读）所需的
> **主机侧资产 + 操作手册**。资产此前只落在两台主机上，此目录把它纳入仓库以求可复现。
> 自动化脚本见 `../scripts/pg-replication.sh`(建/核验/手动提升)与 `../scripts/ha-controller.sh`(自动 failover);
> 方案背景见 [`../HA.md`](../HA.md) §5.1/§5.2。

## 1. 拓扑

```
                      客户端（域名 443）
                            │
                  ┌─────────▼──────────┐
                  │  边缘 Nginx（host-infra，两台各一份） │
                  └────┬───────────┬───┘
                       │           │
          ┌────────────▼──┐   ┌────▼────────────┐
          │ tencent 网关   │   │ aliyun 网关      │   ← 从库只读,网关【停用】待提升
          │ （生产,已切）   │   │ （提升后才可服务）│
          └───────┬───────┘   └────┬────────────┘
                  │                │
          ┌───────▼───────┐   ┌────▼─────────────┐
          │ PG 主（可写）  │──►│ PG 从（只读,hot standby）│
          │ tencent:5432  │   │ aliyun:5432      │
          └───────┬───────┘   └────▲─────────────┘
                  │  发布 loopback 5433 │  经 SSH 隧道回连
                  │                │
                  └────── XS 流复制 ─┘
                     （SSH: aliyun → tencent:127.0.0.1:5433）
```

- **主 = `tencent`**（124.223.188.186，生产；网关 + PG 主）。
- **从 = `aliyun`**（47.116.65.140，热备；PG 只读从库，**网关停用**——从库不可写，跑网关会写失败）。
- 复制走 **SSH 隧道**（`aliyun` → `tencent`），PG 端口**只在主机 loopback 发布**，公网不可达，无需对公网暴露 5432/5433。

> 与 `../HA.md` §2.2 旧表述（aliyun 主 / tencent 从）相反：绿地切换后角色**已对调**，本文为实况。

## 2. 前置（两台 .env）

| 变量 | 两机关系 | 说明 |
|---|---|---|
| `GW_MASTER_KEY` | **必须完全相同** | 渠道密钥密文主密钥；不同则从库解不了数据 |
| `GW_PG_PASSWORD` | **必须完全相同** | `gw` 角色口令；`pg_basebackup` 会把它一并复制到从库，故从库 `POSTGRES_PASSWORD` 必须同值 |
| `GW_REPL_PASSWORD` | 两条独立设置即可 | 复制角色 `repl` 的口令（主库建角色时用，从库 `basebackup`/`primary_conninfo` 用） |

## 3. 主库（`tencent`）一次性配置

`bash pg-replication.sh primary`（在 tencent 上跑）会幂等地：

1. 建复制角色：`CREATE ROLE repl WITH REPLICATION LOGIN PASSWORD '<GW_REPL_PASSWORD>'`。
2. 建物理复制槽：`SELECT pg_create_physical_replication_slot('aliyun')`。
3. `pg_hba.conf` 追加 `host replication all 172.16.0.0/12 scram-sha-256`（放行 docker 网段回连）。
4. `ALTER SYSTEM SET max_slot_wal_keep_size='512MB'`（**安全阀**：从库长期失联时，槽最多积压 512MB WAL 即作废槽，防止主库磁盘被 WAL 撑爆；作废后需重建从库）。

> `wal_level` / `max_wal_senders` / `max_replication_slots` 三者的 PG 16 默认值（`replica` / `10` / `10`）**已足够**，无需改动。
>
> 另需在 `tencent` 宿主放一份 **[`compose-primary.override.yml`](compose-primary.override.yml)** 为 `docker-compose.override.yml`（把 PG 发布到 `127.0.0.1:5433`，供隧道回连）。

## 4. 从库（`aliyun`）一次性配置

前置：`aliyun` 的 `~/.ssh/config` 有 `Host tencent`（HostName `124.223.188.186`，密钥登录）。

`bash pg-replication.sh standby`（在 aliyun 上跑）会：

1. 装并启用隧道单元 [`gw-pg-tunnel.service`](gw-pg-tunnel.service)
   （`autossh -L 0.0.0.0:15432:127.0.0.1:5433 tencent`；绑定 `0.0.0.0` 是因从库容器经
   `host.docker.internal`=docker 网桥网关连入，非 loopback）。
2. 停本机 `db`/`gateway` 容器，**清空 `./pgdata`**（破坏性，脚本要求 `--yes`）。
3. 用一次性 `postgres:16-alpine` 容器跑 `pg_basebackup` 拉基线：

   ```sh
   docker run --rm --add-host host.docker.internal:host-gateway \
     -e PGPASSWORD="$GW_REPL_PASSWORD" \
     -v "$COMPOSE_DIR/pgdata:/var/lib/postgresql/data" \
     postgres:16-alpine \
     pg_basebackup -h host.docker.internal -p 15432 -U repl \
       -D /var/lib/postgresql/data -Fp -Xs -R -P -S aliyun
   ```

   `-R` 会写好 `standby.signal` 与 `primary_conninfo`；脚本再把
   `primary_conninfo`/`primary_slot_name` 覆写为已知良好值（`host.docker.internal port=15432`）。
4. `docker compose up -d db`（**只起 db，不起 gateway**）。
5. 放从库宿主 override [`compose-standby.override.yml`](compose-standby.override.yml)（给 db 加 `extra_hosts: host.docker.internal`）。

**安全组**：`aliyun` 的入站**不放行 15432**（隧道只在主机内供容器回连，公网不需要）。

从库启动后即进入 recovery：`pg_is_in_recovery()=t`、`transaction_read_only=on`。

## 5. 核验

```sh
# 在从库:是否只读从、回放到哪
docker exec ai-gateway-v2-db-1 psql -U gw -d gateway -tA -c \
  "select pg_is_in_recovery(), pg_last_wal_replay_lsn(), pg_last_xact_replay_timestamp();"
docker exec ai-gateway-v2-db-1 psql -U gw -d gateway -c \
  "select status,flushed_lsn,latest_end_lsn from pg_stat_wal_receiver;"

# 在主库:槽是否 active、延迟
docker exec ai-gateway-v2-db-1 psql -U gw -d gateway -c \
  "select slot_name,active,wal_status from pg_replication_slots;"
docker exec ai-gateway-v2-db-1 psql -U gw -d gateway -c \
  "select client_addr,state,sync_state,pg_wal_lsn_diff(sent_lsn,replay_lsn) as lag_bytes from pg_stat_replication;"
```

期望：从库 `t` / `streaming`；主库槽 `active=t`、`state=streaming`、`lag_bytes≈0`。

## 6. 自动 failover（controller，推荐）

从库 `aliyun` 上跑 **`ha-controller.sh`**（systemd timer 每 60s 触发一次 `once`）：主库失联时**自动**
提升本机 → 切 DNS → 飞书告警 → **自锁**。判据用**两条独立信号**（都不经网关），区分「主库真死」与「网络抖」：

| `repl_ok`（`pg_stat_wal_receiver.status=streaming`） | `ssh_ok`（ssh 到 `tencent`） | 动作 |
|---|---|---|
| ✓ | ✓ | 健康，清零失败计数 |
| ✓ | ✗ | 主库 DB 存活（仅探针不通）→ **只告警，不提升** |
| ✗ | ✓ | 主机活、复制断（多隧道/网络）→ **只告警，不提升** |
| ✗ | ✗ | 候选故障 → 计数 +1；连续 **≥ `FAIL_THRESHOLD`**（默认 3）轮 → 提升 |

提升链路（`do_failover`）：

1. **best-effort fence**：尝试 `ssh tencent 'docker compose stop gateway'`（分区时必失败，仅尽力；降低双写窗口）。
2. **提升**：复用 `pg-replication.sh promote --force`（`pg_promote` + 起网关）。
3. **切 DNS**：`dnspod.sh set gateway|gatewayapi <STANDBY_IP>`（TC3，TTL 钳 ≥600）。
4. **自锁**：写 `STATE_FILE`（`.ha-state`）`failed_over=1`；此后每轮直接跳过，**绝不自动回切**。

命令：

```sh
ha-controller.sh status                            # 看判据/计数/锁态（只读）
ha-controller.sh once --dry-run                    # 单轮演练：只判定打印,不提升/不切 DNS/不告警
ha-controller.sh once --dry-run --force-failover   # 演练完整切换链路(打印计划)
ha-controller.sh reset                             # 人工重建完成后清自锁
```

安装：把 §4 的脚本 + 本目录的 `ha-controller.{service,timer}` 装到 aliyun，写 `/opt/ai-gateway-v2/ha.env`
（见 `../ha.env.example`，**不入库**）。**`enable --now ha-controller.timer` 之前，务必先跑 `--dry-run` 并做一次受控演练**
（见下）；auto-failover 一开就会在真故障时自动改 DNS，判据必须确认无误。

**受控演练**（低峰、有人在场）：`ssh tencent 'cd /opt/ai-gateway-v2 && docker compose stop gateway db'` 模拟主死 →
观察连续 3 轮后自动提升 + 切 DNS + 告警；验证 `gateway`/`gatewayapi` 切到 aliyun 后可用。演练后按 §7 的回建流程复位。

**RTO / RPO**：

- **DB 侧 RPO ≈ 复制延迟（秒级）**。
- **客户端 RTO ≈ DNS TTL 上限**：DNSPod 免费版 TTL 下限 **600s** → 最坏 ~10min 才全量切走（期间客户端/LB 缓存逐步失效）。
  DB 与网关本身秒级就绪，**瓶颈在 DNS**。要更快需更好入口（云 LB 健康检查 / 第三节点 / 付费短 TTL 套餐）——**本期不做**。

**残余风险（如实）**：2 节点无 fencing，**分区**（非宕机）时无法证明主库真死，存在脑裂窗口。缓解＝多重判活 + 连续阈值 + 自锁 + best-effort fence；**不追求理论根治**，靠告警让人介入。

## 7. 手动故障切换（兜底 / 演练 / 回建）

**手动、有防脑裂检查**（作为 controller 的兜底，或演练时用）。

触发条件：`tencent` 真的不可用（网络/主机故障）。**提升前必须确认旧主不会再回来写**，否则脑裂。

```sh
# 在 aliyun:确认 tencent 确实不可达,再提升(脚本默认要求 tencent 不可达,--force 可跳过)
bash pg-replication.sh promote            # 或 --force
```

`promote` 做：

1. 守卫：`ssh tencent 'docker ps'` 失败（即旧主不可达）才继续；否则拒绝。
2. `pg_promote`（在从库容器内），等到 `pg_is_in_recovery()=f`。
3. `docker compose up -d gateway`（从库转正后重启网关）。

**紧接着切 DNS**：`bash dnspod.sh set gateway 47.116.65.140` + `set gatewayapi 47.116.65.140`（或控制台人工）。

**回建流程（旧主 `tencent` 恢复后，人工）**——**不可直接重启**，否则其 `restart: unless-stopped` 会自动起 PG → 双主：

```sh
# 1) 在 tencent 上先停栈(别让它自动起)
ssh tencent 'cd /opt/ai-gateway-v2 && docker compose stop gateway db'
# 2) 清掉旧数据目录,以【已转正的 aliyun】为主库重做 basebackup 降级为新从库
#    (复用 primary/standby 的配置反向即可:tencent 建 ssh 到 aliyun 的隧道 + 复制槽/角色)
# 3) 只起 db,校验它处于 recovery
ssh tencent 'cd /opt/ai-gateway-v2 && docker compose up -d db'
ssh tencent 'docker exec ai-gateway-v2-db-1 psql -U gw -d gateway -tA -c "select pg_is_in_recovery();"'  # 期望 t
# 4) 冗余恢复后,在 aliyun 清自锁
ssh aliyun 'bash /opt/ai-gateway-v2/ha-controller.sh reset'
```

> 回建的具体命令待按「反向流复制」逐步固化（当前 `pg-replication.sh` 的 primary/standby 子命令以
> tencent 为主库写死）；在此之前按上述步骤人工执行。**自动回切不做**。

## 8. 回滚到 SQLite（切回迁移前状态）

绿地切换保留了 `aliyun` 上的 SQLite 库 `/opt/ai-gateway-v2/data/gateway-v2.db` 作即时回滚物证。
回滚 = 在 `aliyun` 停 PG 栈、以 SQLite 版二进制起网关、把 DNS 切回 `aliyun`。
**SQLite 与 PG 不双向同步**——回滚会丢窗口内 PG 侧写入（此为绿地方案的既定代价，见 HA.md §9）。
