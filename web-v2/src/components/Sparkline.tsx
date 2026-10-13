interface Props {
  values: number[];
  /** 只给类名与 viewBox，实际宽度由容器拉伸（指标块里铺满卡片宽） */
  className?: string;
  color?: string;
}

/**
 * 迷你趋势线：纯 SVG，无坐标轴、无 tooltip。
 * preserveAspectRatio=none 让它在任意卡片宽度下铺满且不变形标签（因为没有标签）。
 * 颜色默认走品牌色 CSS 变量，随主题切换。
 */
export default function Sparkline({ values, className = 'spark', color }: Props) {
  if (values.length < 2) return <svg className={className} viewBox="0 0 100 28" aria-hidden="true" />;

  const stroke = color ?? 'var(--brand)';
  const max = Math.max(...values);
  const min = Math.min(...values);
  const span = max - min || 1;
  const points = values
    .map((v, i) => `${(i * (100 / (values.length - 1))).toFixed(1)},${(25 - ((v - min) / span) * 22).toFixed(1)}`)
    .join(' ');

  return (
    <svg className={className} viewBox="0 0 100 28" preserveAspectRatio="none" aria-hidden="true">
      <polyline
        points={points}
        fill="none"
        stroke={stroke}
        strokeWidth={1.5}
        vectorEffect="non-scaling-stroke"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}
