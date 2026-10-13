/* ============================================================
   手绘 SVG 图表 —— 移植自原型 chartSvg（线/面积图 + 十字线 + tooltip）。
   配色全部走 CSS 变量，主题切换即时生效，无需重建实例。
   ============================================================ */
import { useEffect, useMemo, useRef, useState } from 'react';

export interface ChartSeries { name: string; data: number[]; color?: string; fill?: boolean }

const PALETTE = ['var(--brand)', 'var(--chart-2)', 'var(--chart-3)', 'var(--err)', 'var(--warn)', 'var(--ok)'];

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log(v) / Math.LN10));
  const f = v / p;
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * p;
}

function fmtCompact(n: number): string {
  if (Math.abs(n) >= 1e9) return (n / 1e9).toFixed(1).replace(/\.0$/, '') + 'B';
  if (Math.abs(n) >= 1e6) return (n / 1e6).toFixed(1).replace(/\.0$/, '') + 'M';
  if (Math.abs(n) >= 1e3) return (n / 1e3).toFixed(1).replace(/\.0$/, '') + 'k';
  return String(Math.round(n));
}

const fmtInt = (n: number) => Math.round(n).toLocaleString('en-US');

export default function Chart({ labels, series, h = 230, yFmt, tipFmt, legend = true }:
  { labels: string[]; series: ChartSeries[]; h?: number; yFmt?: (v: number) => string; tipFmt?: (v: number) => string; legend?: boolean }) {
  const boxRef = useRef<HTMLDivElement | null>(null);
  const [w, setW] = useState(0);
  const [hover, setHover] = useState<{ idx: number; px: number } | null>(null);

  useEffect(() => {
    const el = boxRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setW(Math.max(el.clientWidth || 0, 320)));
    ro.observe(el);
    setW(Math.max(el.clientWidth || 0, 320));
    return () => ro.disconnect();
  }, []);

  const geo = useMemo(() => {
    const W = w || 640;
    const L = 52, R = 8, T = 10, B = 22;
    const iw = W - L - R, ih = h - T - B, n = labels.length;
    let max = 0;
    series.forEach(s => s.data.forEach(v => { if (v > max) max = v; }));
    max = niceMax(max * 1.06) || 1;
    const xAt = (i: number) => L + (n === 1 ? iw / 2 : i * (iw / (n - 1)));
    const y = (v: number) => T + ih - (v / max) * ih;
    return { W, L, R, T, B, iw, ih, n, max, xAt, y };
  }, [w, h, labels.length, series]);

  if (!labels.length || !series.length) return <div ref={boxRef} className="chart" style={{ height: h }} />;

  const { W, L, iw, ih, T, n, max, xAt, y } = geo;

  const gridLines: React.ReactNode[] = [];
  for (let i = 0; i <= 4; i++) {
    const gy = T + ih - (ih * i) / 4;
    gridLines.push(
      <line key={`gl${i}`} x1={L} y1={gy} x2={L + iw} y2={gy} stroke="var(--chart-grid)" strokeWidth={1} />,
      <text key={`gt${i}`} x={L - 7} y={gy + 3} textAnchor="end" fontSize={9} fill="var(--chart-axis)" fontFamily="'JetBrains Mono',monospace">
        {yFmt ? yFmt((max * i) / 4) : fmtCompact((max * i) / 4)}
      </text>,
    );
  }

  const paths: React.ReactNode[] = series.map((s, si) => {
    const color = s.color ?? PALETTE[si % PALETTE.length];
    const d = s.data.map((v, i) => `${i ? 'L' : 'M'}${xAt(i).toFixed(1)} ${y(v).toFixed(1)}`).join('');
    return (
      <g key={si}>
        {s.fill !== false && n > 0 && (
          <path d={`${d}L${xAt(n - 1).toFixed(1)} ${T + ih}L${L} ${T + ih}`} fill={color} fillOpacity={0.10} stroke="none" />
        )}
        <path d={d} fill="none" stroke={color} strokeWidth={1.6} strokeLinejoin="round" strokeLinecap="round" />
      </g>
    );
  });

  const step = Math.ceil(n / 8);
  const xLabels: React.ReactNode[] = [];
  for (let i = 0; i < n; i += step) {
    xLabels.push(
      <text key={`x${i}`} x={xAt(i)} y={h - 6} textAnchor="middle" fontSize={9} fill="var(--chart-axis)" fontFamily="'JetBrains Mono',monospace">
        {labels[i]}
      </text>,
    );
  }

  const onMove = (e: React.MouseEvent) => {
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    let px = e.clientX - rect.left;
    px = Math.max(L, Math.min(L + iw, px));
    let idx = n === 1 ? 0 : Math.round((px - L) / (iw / (n - 1)));
    idx = Math.max(0, Math.min(n - 1, idx));
    setHover({ idx, px: xAt(idx) });
  };

  // tooltip 位置：跟随数据点，右缘溢出时翻到左侧
  let tipLeft: number | null = null;
  let tipFlip = false;
  if (hover) {
    const est = 140;
    tipLeft = hover.px + 12;
    if (tipLeft + est > W - 4) { tipLeft = hover.px - est - 12; tipFlip = true; }
  }

  return (
    <div style={{ width: '100%' }}>
      {legend && series.length > 1 && (
        <div className="legend" style={{ marginBottom: 6 }}>
          {series.map((s, i) => (
            <span key={i} className="legend-item">
              <span className="legend-swatch" style={{ background: s.color ?? PALETTE[i % PALETTE.length] }} />
              {s.name}
            </span>
          ))}
        </div>
      )}
      <div ref={boxRef} className="chart" style={{ height: h }} onMouseMove={onMove} onMouseLeave={() => setHover(null)}>
        <svg viewBox={`0 0 ${W} ${h}`} width="100%" height={h} style={{ display: 'block' }}>
          {gridLines}
          {paths}
          {xLabels}
          {hover && (
            <>
              <line x1={hover.px} y1={T} x2={hover.px} y2={T + ih} stroke="var(--brand)" strokeWidth={1} opacity={0.5} />
              {series.map((s, si) => (
                <circle key={si} cx={hover.px} cy={y(s.data[hover.idx] ?? 0)} r={2.6} fill={s.color ?? PALETTE[si % PALETTE.length]} />
              ))}
            </>
          )}
        </svg>
        {hover && tipLeft !== null && (
          <div className="tip on" style={{ left: tipFlip ? undefined : tipLeft, right: tipFlip ? W - hover.px + 12 : undefined, top: 8 }}>
            <div className="tt">{labels[hover.idx]}</div>
            {series.map((s, si) => (
              <div key={si} className="tr">
                <span>{s.name}</span>
                <b>{tipFmt ? tipFmt(s.data[hover.idx] ?? 0) : fmtInt(s.data[hover.idx] ?? 0)}</b>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
