import { useUi } from '@/stores/ui';

interface Props {
  values: number[];
  width?: number;
  height?: number;
  color?: string;
}

/** 迷你趋势线：纯 SVG，无坐标轴、无 tooltip */
export default function Sparkline({ values, width = 96, height = 28, color }: Props) {
  const theme = useUi(s => s.theme);
  const stroke = color ?? (theme === 'dark' ? '#3B82F6' : '#2563EB');
  const pad = 2;
  const max = Math.max(...values, 1);
  const min = Math.min(...values, 0);
  const step = (width - pad * 2) / Math.max(values.length - 1, 1);
  const d = values
    .map((v, i) => {
      const x = pad + i * step;
      const y = height - pad - ((v - min) / (max - min || 1)) * (height - pad * 2);
      return `${i ? 'L' : 'M'}${x.toFixed(1)} ${y.toFixed(1)}`;
    })
    .join(' ');

  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} aria-hidden>
      <path d={d} fill="none" stroke={stroke} strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" opacity={0.75} />
    </svg>
  );
}
