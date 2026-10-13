/**
 * 图表配色 —— 全部返回 CSS 变量字符串，随 data-theme 即时切换。
 * 主色走靛蓝，语义色（错误/警告/成功）独立，多系列用分类色板。
 */
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
  series: string[];
}

export const CHART_COLORS: ChartColors = {
  text: 'var(--chart-axis)',
  line: 'var(--chart-grid)',
  primary: 'var(--brand)',
  primarySoft: 'var(--chart-2)',
  error: 'var(--err)',
  warn: 'var(--warn)',
  ok: 'var(--ok)',
  tooltipBg: 'var(--panel)',
  tooltipBorder: 'var(--line)',
  series: ['var(--brand)', 'var(--chart-2)', 'var(--ok)', 'var(--warn)', 'var(--err)', 'var(--chart-3)'],
};

export function useChartColors(): ChartColors {
  return CHART_COLORS;
}
