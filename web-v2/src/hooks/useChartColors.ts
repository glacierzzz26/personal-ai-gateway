import { useUi } from '@/stores/ui';

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

const LIGHT: ChartColors = {
  text: '#94A3B8', line: '#EFF1F4', primary: '#2563EB', primarySoft: '#93C5FD',
  error: '#EF4444', warn: '#F59E0B', ok: '#16A34A',
  tooltipBg: '#FFFFFF', tooltipBorder: '#E5E7EB',
};

const DARK: ChartColors = {
  text: '#64748B', line: '#1B2130', primary: '#3B82F6', primarySoft: '#93C5FD',
  error: '#F87171', warn: '#FBBF24', ok: '#22C55E',
  tooltipBg: '#1F2839', tooltipBorder: '#232936',
};

export function useChartColors(): ChartColors {
  return useUi(s => s.theme) === 'dark' ? DARK : LIGHT;
}
