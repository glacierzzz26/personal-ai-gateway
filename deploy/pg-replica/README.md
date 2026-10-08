# PG 主从流复制（双云 HA）

> 本目录是把 `aliyun` 作为 **`tencent` 的 PostgreSQL 流复制热备**（standby，只读）所需的
> **主机侧资产 + 操作手册**。资产此前只落在两台主机上，此目录把它纳入仓库以求可复现。
> 自动化脚本见 `../scripts/pg-replication.sh`；方案背景见 [`../HA.md`](../HA.md) §5.1/§5.2。

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

## 6. 故障切换（提升从库）

**手动、有防脑裂检查**（自动 failover 未做，见 HA.md §8 P3）。

触发条件：`tencent` 真的不可用（网络/主机故障）。**提升前必须确认旧主不会再回来写**，否则脑裂。

```sh
# 在 aliyun:确认 tencent 确实不可达,再提升(脚本默认要求 tencent 不可达,--force 可跳过)
bash pg-replication.sh promote            # 或 --force
```

`promote` 做：

1. 守卫：`ssh tencent 'docker ps'` 失败（即旧主不可达）才继续；否则拒绝。
2. `pg_ctl promote`（在从库容器内），等到 `pg_is_in_recovery()=f`。
3. `docker compose up -d gateway`（从库转正后重启网关）。

**紧接着切 DNS**（人工/控制台或 TC3 API，见 [`../HA.md`](../HA.md) §5.1）：
`gateway.5home.online` 与 `gatewayapi.5home.online` 的 A 记录 → `47.116.65.140`。

> 旧主 `tencent` 恢复后**不可直接重启**——它的 PG 仍是旧主，会脑裂。必须
> **`pg_rewind` 或重做 `pg_basebackup`** 把它降级为新从库，再 `up -d db`。恢复前保持其网关停用。

## 7. 回滚到 SQLite（切回迁移前状态）

绿地切换保留了 `aliyun` 上的 SQLite 库 `/opt/ai-gateway-v2/data/gateway-v2.db` 作即时回滚物证。
回滚 = 在 `aliyun` 停 PG 栈、以 SQLite 版二进制起网关、把 DNS 切回 `aliyun`。
**SQLite 与 PG 不双向同步**——回滚会丢窗口内 PG 侧写入（此为绿地方案的既定代价，见 HA.md §9）。
