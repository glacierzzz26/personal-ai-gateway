import { TOKENS } from '@/styles/tokens';

export interface ChartColors {
  text: string;
  line: string;
  primary: string;
  primarySoft: string;
  error: string;
  warn: string;
  ok: string;
  tooltipBg: string;
  tooltipBorder: string;
}

/**
 * 图表配色 —— 只允许规范内的四段同色相青蓝 + 语义色。
 * 已无深色模式：单一配色，不再随主题切换。
 */
export const CHART_COLORS: ChartColors = {
  text: TOKENS.aux,
  line: TOKENS.border,
  primary: TOKENS.c1,
  primarySoft: TOKENS.c2,
  error: TOKENS.err,
  warn: TOKENS.warn,
  ok: TOKENS.ok,
  tooltipBg: TOKENS.card,
  tooltipBorder: TOKENS.border,
};

export function useChartColors(): ChartColors {
  return CHART_COLORS;
}
