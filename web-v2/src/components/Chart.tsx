/**
 * 手绘 SVG 图表 —— 移植自原型 prototype-webv2.html 的 chartSvg / niceMax，
 * 并扩展到 web-v2 实际所需：柱状(单/分组)、双 Y 轴、面积填充、十字准星 tooltip、随容器宽度自适应。
 * 颜色一律用 CSS 变量字符串（'var(--brand)' 等），随主题即时变化。
 */
import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import clsx from 'clsx';
import { fmt } from '@/utils/format';

export interface ChartSeries {
  name: string;
  data: number[];
  /** CSS 颜色字符串（建议 var(--x)） */
  color: string;
  kind?: 'line' | 'bar';
  axis?: 'left' | 'right';
  /** 面积填充（仅 line） */
  fill?: boolean;
  /** tooltip 单值格式化 */
  fmt?: (v: number) => string;
}

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log(v) / Math.LN10));
  const f = v / p;
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * p;
}

export function Chart({
  labels,
  series,
  height = 230,
  leftFmt,
  rightFmt,
  yMinZero = true,
}: {
  labels: string[];
  series: ChartSeries[];
  height?: number;
  leftFmt?: (v: number) => string;
  rightFmt?: (v: number) => string;
  yMinZero?: boolean;
}) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const [w, setW] = useState(600);
  const [hover, setHover] = useState<number | null>(null);

  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setW(Math.max(el.clientWidth, 320)));
    ro.observe(el);
    setW(Math.max(el.clientWidth, 320));
    return () => ro.disconnect();
  }, []);

  const W = Math.max(w, 320);
  const H = height;
  const hasRight = series.some((s) => s.axis === 'right');
  const L = 52;
  const R = hasRight ? 46 : 10;
  const T = 10;
  const B = 22;
  const iw = W - L - R;
  const ih = H - T - B;
  const n = labels.length;

  const maxOf = (axis: 'left' | 'right') => {
    let m = 0;
    series
      .filter((s) => (s.axis ?? 'left') === axis)
      .forEach((s) => s.data.forEach((v) => { if (v > m) m = v; }));
    return niceMax(m * 1.06) || 1;
  };
  const maxL = maxOf('left');
  const maxR = maxOf('right') || 1;

  const centered = series.some((s) => s.kind === 'bar');
  const slot = iw / Math.max(n, 1);
  const xAt = (i: number) => (centered ? L + (i + 0.5) * slot : L + (n === 1 ? iw / 2 : i * (iw / (n - 1))));
  const yAxis = (axis: 'left' | 'right') => (v: number) => {
    const max = axis === 'right' ? maxR : maxL;
    const base = yMinZero ? 0 : -max;
    return T + ih - ((v - base) / (max - base)) * ih;
  };

  const barsLeft = series.filter((s) => s.kind === 'bar' && (s.axis ?? 'left') === 'left').length;
  const barsRight = series.filter((s) => s.kind === 'bar' && s.axis === 'right').length;

  /* --- 网格 + 轴刻度 --- */
  const grid: ReactNode[] = [];
  for (let i = 0; i <= 4; i++) {
    const gy = T + ih - (ih * i) / 4;
    grid.push(<line key={`g${i}`} x1={L} y1={gy} x2={L + iw} y2={gy} stroke="var(--chart-grid)" strokeWidth={1} />);
    grid.push(
      <text key={`gl${i}`} x={L - 7} y={gy + 3} textAnchor="end" fontSize={9} fill="var(--chart-axis)" fontFamily="JetBrains Mono,monospace">
        {(leftFmt ?? fmt.k)((maxL * i) / 4)}
      </text>,
    );
    if (hasRight) {
      grid.push(
        <text key={`gr${i}`} x={L + iw + 7} y={gy + 3} textAnchor="start" fontSize={9} fill="var(--chart-axis)" fontFamily="JetBrains Mono,monospace">
          {(rightFmt ?? fmt.k)((maxR * i) / 4)}
        </text>,
      );
    }
  }

  /* --- 数据体 --- */
  const body: ReactNode[] = [];
  const barSeenL = { v: 0 };
  const barSeenR = { v: 0 };
  series.forEach((s, si) => {
    const axis = s.axis ?? 'left';
    const y = yAxis(axis);
    if (s.kind === 'bar') {
      const groupCount = axis === 'right' ? barsRight : barsLeft;
      const seen = axis === 'right' ? barSeenR : barSeenL;
      const gi = seen.v++;
      const bw = Math.min((slot / groupCount) * 0.62, 28);
      const centerX = (i: number) => xAt(i) - (groupCount * bw) / 2 + gi * bw + bw / 2;
      body.push(
        <g key={`b${si}`}>
          {s.data.map((v, i) => {
            const yy = y(v);
            const base = y(0);
            const top = Math.min(yy, base);
            const h = Math.abs(base - yy);
            return <rect key={i} x={centerX(i) - bw / 2} y={top} width={bw} height={Math.max(h, v > 0 ? 1 : 0)} fill={s.color} rx={1.5} />;
          })}
        </g>,
      );
      return;
    }
    // line / area
    let d = '';
    s.data.forEach((v, i) => {
      d += (i ? 'L' : 'M') + xAt(i).toFixed(1) + ' ' + y(v).toFixed(1);
    });
    if (s.fill) {
      body.push(
        <path key={`f${si}`} d={`${d}L${xAt(n - 1).toFixed(1)} ${T + ih}L${xAt(0).toFixed(1)} ${T + ih}`} fill={s.color} fillOpacity={0.1} />,
      );
    }
    body.push(
      <path key={`l${si}`} d={d} fill="none" stroke={s.color} strokeWidth={1.6} strokeLinejoin="round" strokeLinecap="round" />,
    );
  });

  /* --- x 轴标签 --- */
  const xlabels: ReactNode[] = [];
  const step = Math.ceil(n / 8);
  for (let i = 0; i < n; i += step) {
    xlabels.push(
      <text key={i} x={xAt(i).toFixed(1)} y={H - 6} textAnchor="middle" fontSize={9} fill="var(--chart-axis)" fontFamily="JetBrains Mono,monospace">
        {labels[i]}
      </text>,
    );
  }

  /* --- 交互 --- */
  const onMove = (e: React.MouseEvent) => {
    const el = wrapRef.current;
    if (!el || !n) return;
    const rect = el.getBoundingClientRect();
    const px = Math.max(L, Math.min(L + iw, e.clientX - rect.left));
    const idx = centered
      ? Math.max(0, Math.min(n - 1, Math.floor((px - L) / slot)))
      : Math.max(0, Math.min(n - 1, Math.round((px - L) / (iw / Math.max(n - 1, 1)))));
    setHover(idx);
  };

  let tipLeft = 0;
  if (hover != null) {
    const tipW = 150;
    tipLeft = xAt(hover) + 12;
    if (tipLeft + tipW > W - 4) tipLeft = xAt(hover) - tipW - 12;
    tipLeft = Math.max(2, tipLeft);
  }

  return (
    <div
      ref={wrapRef}
      className="chart"
      style={{ height: H }}
      onMouseMove={onMove}
      onMouseLeave={() => setHover(null)}
    >
      <svg viewBox={`0 0 ${W} ${H}`} width={W} height={H} style={{ display: 'block' }}>
        {grid}
        {body}
        {xlabels}
      </svg>
      {hover != null && n > 0 && (
        <>
          <div className={clsx('crosshair', 'on')} style={{ left: xAt(hover) }} />
          <div className={clsx('tip', 'on')} style={{ left: tipLeft, top: 8 }}>
            <div className="tt">{labels[hover]}</div>
            {series.map((s, i) => (
              <div className="tr" key={i}>
                <span>{s.name}</span>
                <b>{(s.fmt ?? leftFmt ?? fmt.n)(s.data[hover] ?? 0)}</b>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

export function ChartLegend({ items }: { items: { label: string; color: string }[] }) {
  return (
    <div className="legend">
      {items.map((it, i) => (
        <span className="legend-item" key={i}>
          <span className="legend-swatch" style={{ background: it.color }} />
          {it.label}
        </span>
      ))}
    </div>
  );
}

// 各页以默认导入方式引用图表(`import Chart, { ChartLegend } from '@/components/Chart'`)。
export default Chart;
