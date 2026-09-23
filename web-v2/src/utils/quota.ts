import type { ChannelQuota, QuotaWindowKey } from '@/types';

/**
 * 额度口径(令牌额度 / 上游渠道额度共用一处)。
 *
 * 两档语义固定:
 *   逼近 = ≥60%（概览「额度逼近」块用它，只是提醒「有风险」）；
 *   告警 = ≥85%（顶栏状态条、渠道页高亮用它，代表「快断粮」）。
 *
 * **不要在页面里各写一套阈值** —— 同一个额度在两个页面显示成不同的严重程度，
 * 会被当成两个 bug 反复排查。渠道页历史上就踩过一次（50/80% vs 60/85%）。
 */
export const QUOTA_WARN = 0.6;
export const QUOTA_ALERT = 0.85;

/** 渠道额度窗口的展示顺序（rolling≈近 5h）。 */
export const QUOTA_WINS: QuotaWindowKey[] = ['rolling', 'weekly', 'monthly'];

/** 上游能给出「已用/上限」原始量的窗口（one-api / new-api 的 monthly 给得出）。 */
export function hasCap(q: ChannelQuota | undefined, key: QuotaWindowKey): boolean {
  const cap = q?.windows?.[key]?.cap;
  return cap != null && cap > 0;
}

/** 该渠道有没有任何可用(status==='ok')的窗口。 */
export function hasOkWindow(q: ChannelQuota | undefined): boolean {
  return QUOTA_WINS.some(k => q?.windows?.[k]?.status === 'ok');
}

/**
 * 额度「余量比率」= 各可用窗口已用率的**最大值**(0..1)。余额型(无窗口)返回 null。
 *
 * 排序 / 告警判定 / 高亮 / 筛选统一用它 —— 一处口径，避免多处各算各的对不上。
 * 返回 null 表示**无从判断**（未配置、查询失败、无窗口），调用方必须把它当作
 * 「不是告警」而不是「告警」：查不到 ≠ 快没额度了。
 */
export function quotaRatio(q: ChannelQuota | undefined): number | null {
  const pcts = QUOTA_WINS.flatMap(k => {
    const w = q?.windows?.[k];
    return w && w.status === 'ok' ? [w.percent] : [];
  });
  if (!pcts.length) return null;
  return Math.max(...pcts) / 100;
}

/** 额度告警级别：达到告警阈值('alert')、逼近阈值('warn')、正常('ok')、无从判断(null)。 */
export function quotaTone(q: ChannelQuota | undefined): 'alert' | 'warn' | 'ok' | null {
  const r = quotaRatio(q);
  if (r == null) return null;
  if (r >= QUOTA_ALERT) return 'alert';
  return r >= QUOTA_WARN ? 'warn' : 'ok';
}

/** 百分比展示：整数不带小数，否则保留 1 位。 */
export const pctText = (n: number): string => (Number.isInteger(n) ? String(n) : n.toFixed(1));
