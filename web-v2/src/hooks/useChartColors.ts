/* ============================================================
   图表配色 —— 全部走 CSS 变量，双主题即时切换（无 JS 色值）。
   series 色板顺序与原型一致：靛蓝 → 浅靛 → 深靛 → 语义色。
   ============================================================ */
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
  series: ['var(--brand)', 'var(--chart-2)', 'var(--chart-3)', 'var(--err)', 'var(--warn)', 'var(--ok)'],
};

export function useChartColors(): ChartColors {
  return CHART_COLORS;
}
