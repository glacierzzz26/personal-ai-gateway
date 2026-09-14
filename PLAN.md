# 改造计划:从「个人网关」到「中转站」

> 本文件是 2026-09-14 定稿的改造方案,**尚未实施**。目标:把这个自用网关改成能对外卖的中转站
> (形似 New API,但**砍掉企业级**:无自助注册/邮件/支付/团队/发票)。
> 风险与漏钱的现网 bug 见 §1;分阶段验收见 §7。

## 1. 现状(实测)

| 项 | 实测 |
|---|---|
| 角色 | `admin` / `user` 两档已在跑:`ListTokens(owner)`、`loadManageableToken` 越权返 404、前端 `App.tsx` 的 `AdminOnly` 三重收口 |
| **漏收费(真金白银)** | 入口预检查 `used_usd >= quota_usd`(`gateway.go:172`),结算 `ChargeToken` 却带「不超过上限」条件、越界返错,而两个调用点都 `_ =` 吞掉(`gateway.go:445`/`:563`)。**剩余额度不足一笔时:不拒、也不计费,`used_usd` 永不前进 → 无限跑下去。** |
| 额度语义 | `quota_usd` 挂在**令牌**上,`used_usd` **只增不减**(`UpdateToken` 不碰它);无「重置/续期」,续额度只能删了重建(历史同亡) |
| 无用户级账户 | `admins` 无余额概念;无钱包、无充值、无账变流水 |
| 币种 | 全站单一币种 `settings.displayCurrency`;`model_offers.*_price_usd` / `official_prices.*_price` 是**历史命名**,装的是「当前计价币种金额」;`costUsd()` 直接乘 token,**不做换算**(`gateway.go:632`) |
| 对外暴露面 | `GET /api/v1/models` **登录即可**(`server.go:153`),对客户**全量**返回渠道名、每个供给源的上游真实名、官方价来源 URL、以及**全站** `todayRequests/successRate`(`reads.go:136-177`) |
| 官方价覆盖 | `pricing.Supports()` 只放行 DeepSeek / 通义 / 智谱(`pricing.go:57`)。**Anthropic / OpenAI 不在表里 → 抓不了,`BuildManual` 也被挡 → Claude/GPT 的官方价根本录不进库** |

## 2. 定价模型(定稿:就 3 个数)

> 用户口中的「中转站售价」=「国内售价」= **本站卖给客户的价**,同一个数。整盘生意只有 3 个数:

| 数 | 含义 | 存哪 | 币种 | 谁可见 |
|---|---|---|---|---|
| **官方价** | 厂商官网挂牌(如 Anthropic `$3/$15`,**输入/输出,每百万 token**) | `official_prices`(已有) | USD 原币 | 用户(划线原价) |
| **本站价** = 官方价 × 倍率 | 你卖给客户的价 | **现算**,不落库(改倍率即时生效) | ¥ | 用户 |
| **成本** | 你实付上游(command code ai)的钱 | `model_offers.*_price`(已有) | ¥ | **仅 admin** |

- **倍率**:`settings.price_multiplier`(全局默认);`model_offers.rate_override`(可空,单模型覆盖)—— 仿现有 `override_price` 套路。本期只做全局,单模型字段留而不用。
- **毛利** = 本站价 − 成本,**只在管理面**出现,绝不出现在用户面。
- **换汇**:官方价是 USD;算售价时过 `settings.USDPerCNY`(已有)。汇率为 0 = 未设 → **拒绝计算并提示**,不臆造(与 `convertPrice` 同规矩)。

### 用户侧展示(两行)

```
claude-sonnet-5              输入 / 输出 (每百万)
──────────────────────────────────────────────
官方价   ¥21   / ¥105    (划线)
本站价   ¥10.5 / ¥52.5     0.5× 官方价
后台另存:你的成本 ¥8.4 / ¥42(仅你可见)
```

## 3. 数据模型(迁移 v7)

```sql
-- 钱包:只有 role=user 扣钱;admin(你自己)不扣
ALTER TABLE admins ADD COLUMN balance_usd  REAL NOT NULL DEFAULT 0;
ALTER TABLE admins ADD COLUMN rate_override REAL;                 -- NULL = 用全局倍率

-- 账变审计(钱包不能只有当前值、没有流水)
CREATE TABLE balance_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  admin_id      INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
  delta         REAL    NOT NULL,          -- 正=充值,负=扣费
  balance_after REAL    NOT NULL,
  reason        TEXT    NOT NULL,          -- charge | topup | adjust
  log_id        INTEGER NOT NULL DEFAULT 0,-- 关联 request_logs.id
  note          TEXT    NOT NULL DEFAULT '',
  created_at    TEXT    NOT NULL
);

-- 客户付你的钱:cost 保持 = 成本,新增 charge = 售价
ALTER TABLE request_logs ADD COLUMN charge_usd REAL    NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN owner_id   INTEGER NOT NULL DEFAULT 0;  -- 冗余归属,免 JOIN
UPDATE request_logs SET owner_id = COALESCE(
  (SELECT t.owner_id FROM tokens t WHERE t.id = request_logs.token_id), 0);
CREATE INDEX idx_logs_owner ON request_logs (owner_id, ts);
```

`settings` 新增键 `price_multiplier`(默认 `1.0`),进 `domain.Settings` 与「系统设置」页。

## 4. 计费链路

出站成功只有两处落账:`finish`(非流,`gateway.go:443`)与 `streamFrom`(流,`gateway.go:561`)。两者都:

```
cost   = costUsd(offer, tok)                          // 你付上游(不变)
rate   = owner.rate_override ?? settings.price_multiplier
charge = cost × rate                                   // 客户付你
→ 单事务 SettleRequest{ InsertLog(charge_usd,owner_id) + ChargeToken(charge) + 扣钱包 + 写 balance_logs }
```

- **事务化**:现在 log 与 charge 是两笔独立写,失败会账实不符。改为 store 层单个 `SettleRequest`,单事务落 log + 扣余额 + 令牌累加 + 账变。
- **谁扣**:`owner` 是 `user` → 扣钱包;`owner` 是 `admin` 或 NULL(历史全局 key)→ **只记账不扣钱**(「你自己」天然免疫)。
- **余额门禁**:`parseInbound` 对 user 归属令牌加「余额 ≤ 0 → 402」,与 `tokenGateErr` 并列。**允许透支一笔**,不做预冻结(2C2G 量级并发敞口可忽略,记入已知坑)。
- **`quota_usd` 降级为令牌子预算**(分闸),主闸是钱包。

## 5. 可见面(角色仍是 admin|user,变的是每面返回什么)

| 面 | admin(你) | user(客户) |
|---|---|---|
| `GET /models` | 全量 | **收敛**:仅启用 ∩ `allowed_models`;只留 对外名/上下文/能力/**官方价+本站价**;剥渠道名、`upstreamModel`、`priceSourceUrl`、成本、全站 `todayRequests/successRate` |
| `/overview` `/usage` `/logs` | 全站 | 新增 `/api/v1/me/*`(owner 维度) |
| 钱包 | 给任意用户充值 / 调倍率 | `GET /me/balance` + 账变流水 |
| 令牌 | 任意用户 | 自助建,**但不得超过管理员设的用户上限(ceiling)** |

前端:`Home()`(`App.tsx:31`)现在非管理员一律弹 `/tokens` → 改为弹新的 **`/me`(我的用量:余额 + 支出曲线 + 按模型分布 + 最近请求)**;`nav.tsx` 加一条非 `adminOnly` 项;`Tokens.tsx` 额度条改为「余额进度」为主;`Users.tsx` 加余额/充值/倍率。

## 6. 明确不做(省掉的正是「企业级」)

自助注册、邮件验证、支付网关、兑换码、组织/团队 RBAC、发票、分组倍率。开号、充值、调价一律 admin 手工。

## 7. 分阶段与验收

| 阶段 | 内容 | 验收 |
|---|---|---|
| **S0** | 修 `ChargeToken` 漏收费:去掉 quota 条件、无条件累加;入口预检查成为唯一拒绝点 | 剩余额度不足一笔时**稳定 402 且已计费**,不再无限跑 |
| **S1** | 迁移 v7 + `SettleRequest` 单事务 + 钱包扣减 + 倍率 | 一个 user key 跑请求 → 余额按售价下降、`balance_logs` 有流水、`cost ≠ charge` |
| **S2** | admin 充值/调倍率接口 + `Users.tsx`;`/me/balance`、`/me/usage`、`/me/logs` + 「我的用量」页 + 角色导航 | 客户登录只见「我的用量 / 访问令牌」,看得到余额与自己的请求 |
| **S3** | `GET /models` 分角色收敛 + 用户令牌上限 ceiling | 客户 token 看到的模型无渠道/上游/来源/成本字段 |
| **S4** | **放开手工录入的厂商白名单**(Claude/GPT 官方价现在录不进)+ Cmd+K 角色过滤 + 用户侧币种 + 阈值提醒 | issue #8 的 P1/P2 清单收敛 |

> **S4 的放开是 S3 展示的前置**:没有官方价就没得乘。

## 8. 已知坑

- **币种是单值**:官方价原币 USD,售价 ¥ —— 展示/计算必须显式过 `USDPerCNY`;汇率为 0 时拒绝而非默认 1。
- **`_usd` 是历史命名**:语义已是「当前计价币种金额」,不要按名字当美元处理(`gateway.go:632` 不做换算)。
- **透支敞口**:不预冻结 → 单令牌最大损失 = 一笔请求的成本(远好于现状的「无穷」)。
- **历史全局 key**:`owner_id IS NULL` 的令牌不扣任何钱包,只记账。
- **Anthropic 定价页 JS 渲染**:加抓取器大概率白做;手工录入 + 来源 URL 必填即可。

## 参考

`DESIGN.md`(架构)、`deploy/DR.md`(灾备,并行线)、`internal/proxy/gateway.go`(计费)、`internal/store/tokens.go`(额度)、`internal/server/reads.go`(展示聚合)、issue #8(普通用户视角)。
