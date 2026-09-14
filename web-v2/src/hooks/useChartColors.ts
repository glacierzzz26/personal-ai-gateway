import { TOKENS } from '@/styles/tokens';

export interface ChartColors {
  text: string;
  line: string;
  /** 主色(靛蓝)—— 单主指标(请求数)的柱/线 */
  primary: string;
  /** 主色浅一档 —— 同指标两系列对比时的次色 */
  primarySoft: string;
  error: string;
  warn: string;
  ok: string;
  tooltipBg: string;
  tooltipBorder: string;
  /** 分类色板 —— 多系列(按模型/渠道/令牌)对比时逐系列取色,系列间一眼可分 */
  series: string[];
}

/**
 * 图表配色 —— 主色走靛蓝,语义色(错误/警告/成功)独立,
 * 多系列用分类色板。已无深色模式:单一配色,不随主题切换。
 */
export const CHART_COLORS: ChartColors = {
  text: TOKENS.aux,
  line: TOKENS.border,
  primary: TOKENS.primary,
  primarySoft: TOKENS.primarySoft,
  error: TOKENS.err,
  warn: TOKENS.warn,
  ok: TOKENS.ok,
  tooltipBg: TOKENS.card,
  tooltipBorder: TOKENS.border,
  series: [TOKENS.c1, TOKENS.c2, TOKENS.c3, TOKENS.c4, TOKENS.c5, TOKENS.c6],
};

export function useChartColors(): ChartColors {
  return CHART_COLORS;
}
