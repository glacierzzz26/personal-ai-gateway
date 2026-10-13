/* ============================================================
   自绘 UI 原语 —— 与 prototype.css 的类一一对应。
   页面只允许使用本文件与 ui/ 下的组件，不允许再引 antd。
   ============================================================ */
import { useEffect, useRef, useState } from 'react';
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode, SelectHTMLAttributes } from 'react';
import { Icon } from './icons';
import { copyText } from '@/utils/clipboard';
import { toast } from './toast';

/* ---------------- Button ---------------- */
type BtnVariant = 'primary' | 'secondary' | 'ghost' | 'danger';
export function Btn({ variant = 'secondary', size, icon, children, loading, danger, block, ...rest }:
  ButtonHTMLAttributes<HTMLButtonElement> & { variant?: BtnVariant; size?: 'sm'; icon?: string; loading?: boolean; danger?: boolean; block?: boolean }) {
  return (
    <button
      {...rest}
      disabled={rest.disabled || loading}
      className={`btn btn-${danger ? 'danger' : variant}${size === 'sm' ? ' btn-sm' : ''}${block ? ' btn-block' : ''}${rest.className ? ' ' + rest.className : ''}`}
      style={block ? { width: '100%', ...(rest.style ?? {}) } : rest.style}
    >
      {loading ? <Icon n="refresh" /> : icon ? <Icon n={icon} /> : null}
      {children}
    </button>
  );
}

export function IconBtn({ n, title, onClick, className }:
  { n: string; title?: string; onClick?: (e: React.MouseEvent) => void; className?: string }) {
  return (
    <button type="button" className={`icon-btn${className ? ' ' + className : ''}`} title={title} onClick={onClick}>
      <Icon n={n} />
    </button>
  );
}

/* ---------------- Segmented ---------------- */
export function Seg<T extends string>({ value, onChange, options, sm }:
  { value: T; onChange: (v: T) => void; options: { v: T; label: ReactNode }[]; sm?: boolean }) {
  return (
    <div className={`seg${sm ? ' seg-sm' : ''}`}>
      {options.map(o => (
        <button key={o.v} className={value === o.v ? 'on' : ''} onClick={() => onChange(o.v)}>{o.label}</button>
      ))}
    </div>
  );
}

/* ---------------- Card ---------------- */
export function Card({ title, icon, cnt, extra, note, children, bodyClass, className, onClick, style }:
  { title?: ReactNode; icon?: string; cnt?: ReactNode; extra?: ReactNode; note?: ReactNode; children?: ReactNode; bodyClass?: string; className?: string; onClick?: () => void; style?: React.CSSProperties }) {
  return (
    <div className={`card${className ? ' ' + className : ''}`} onClick={onClick} style={style}>
      {title !== undefined && (
        <div className="card-head">
          <div className="card-title">{icon ? <Icon n={icon} /> : null}{title}{cnt !== undefined && cnt !== null ? <span className="cnt">{cnt}</span> : null}</div>
          {extra}
        </div>
      )}
      {note ? <div style={{ padding: '8px 13px 0' }}><span className="card-note">{note}</span></div> : null}
      <div className={bodyClass === null ? '' : bodyClass ?? 'card-body'}>{children}</div>
    </div>
  );
}

/* ---------------- KPI ---------------- */
export function Kpi({ label, value, unit, trend, spark, foot }:
  { label: ReactNode; value: ReactNode; unit?: string; trend?: { dir: 'up' | 'down'; good?: boolean; cmp?: string }; spark?: ReactNode; foot?: ReactNode }) {
  return (
    <div className="card kpi">
      <div className="kpi-label">{label}</div>
      <div className="kpi-value">{value}{unit ? <span className="unit">{unit}</span> : null}</div>
      {(trend || spark || foot) && (
        <div className="kpi-foot">
          {trend ? (
            <span className={`trend ${trend.dir}${trend.good !== undefined ? (trend.good ? ' good' : ' bad') : ''}`}>
              <Icon n={trend.dir === 'up' ? 'upRight' : 'downRight'} />
              {trend.dir === 'up' ? '↑' : '↓'}
              {trend.cmp ? <span className="cmp">{trend.cmp}</span> : null}
            </span>
          ) : <span />}
          {foot}
          {spark}
        </div>
      )}
    </div>
  );
}

/* ---------------- Badge / Tag / StatusDot ---------------- */
export type Tone = 'brand' | 'brand-o' | 'green' | 'amber' | 'red' | 'slate';
export function Badge({ tone = 'slate', children, icon, title }:
  { tone?: Tone; children: ReactNode; icon?: string; title?: string }) {
  return <span className={`badge b-${tone}`} title={title}>{icon ? <Icon n={icon} /> : null}{children}</span>;
}

export function StatusDot({ tone }: { tone: 'g' | 'a' | 'r' | 's' }) {
  return <span className={`dot ${tone}`} />;
}

/* ---------------- 表单 ---------------- */
export function Field({ label, hint, err, children, style }:
  { label?: ReactNode; hint?: ReactNode; err?: ReactNode; children: ReactNode; style?: React.CSSProperties }) {
  return (
    <div className="field" style={style}>
      {label ? <label>{label}</label> : null}
      {children}
      {hint && !err ? <span className="hint">{hint}</span> : null}
      {err ? <span className="err-text">{err}</span> : null}
    </div>
  );
}

export const Input = (p: InputHTMLAttributes<HTMLInputElement> & { err?: boolean }) => (
  <input {...p} className={`input${p.err ? ' err' : ''}${p.className ? ' ' + p.className : ''}`} />
);

export const Select = ({ children, ...p }: SelectHTMLAttributes<HTMLSelectElement> & { err?: boolean }) => (
  <select {...p} className={`select${p.err ? ' err' : ''}${p.className ? ' ' + p.className : ''}`}>{children}</select>
);

export function Switch({ checked, onChange, label, disabled, loading }:
  { checked: boolean; onChange: (v: boolean) => void; label?: ReactNode; disabled?: boolean; loading?: boolean }) {
  return (
    <label className="switch" style={disabled || loading ? { opacity: .5, pointerEvents: 'none' } : undefined}>
      <input type="checkbox" checked={checked} onChange={e => onChange(e.target.checked)} />
      <span className="track" />
      {label ? <span className="sw-label">{label}</span> : null}
    </label>
  );
}

export function Check({ checked, onChange, children }:
  { checked: boolean; onChange: (v: boolean) => void; children: ReactNode }) {
  return (
    <label className="cb">
      <input type="checkbox" checked={checked} onChange={e => onChange(e.target.checked)} />
      <span className="box"><Icon n="check" /></span>
      <span>{children}</span>
    </label>
  );
}

/* ---------------- 表格辅助 ---------------- */
export function SortInd({ dir }: { dir: 'asc' | 'desc' | null }) {
  return (
    <span className={`sort-ind${dir ? ' on' : ''}`}>
      {dir === 'asc' ? <Icon n="up" /> : dir === 'desc' ? <Icon n="down" /> : <Icon n="down" style={{ opacity: .35 }} />}
    </span>
  );
}

export function Th({ children, right, sortable, dir, onClick }:
  { children?: ReactNode; right?: boolean; sortable?: boolean; dir?: 'asc' | 'desc' | null; onClick?: () => void }) {
  return (
    <th className={`${right ? 'right' : ''}${sortable ? ' sortable' : ''}`} onClick={onClick}>
      {children}
      {sortable ? <SortInd dir={dir ?? null} /> : null}
    </th>
  );
}

/* ---------------- 分页（原型 pagerHtml 同款页码折叠） ---------------- */
function pageList(page: number, pages: number): (number | '…')[] {
  if (pages <= 7) return Array.from({ length: pages }, (_, i) => i + 1);
  const out: (number | '…')[] = [1];
  const a = Math.max(2, page - 1), b = Math.min(pages - 1, page + 1);
  if (a > 2) out.push('…');
  for (let i = a; i <= b; i++) out.push(i);
  if (b < pages - 1) out.push('…');
  out.push(pages);
  return out;
}

export function Pager({ page, size, total, onPage }:
  { page: number; size: number; total: number; onPage: (p: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / size));
  if (total === 0) return null;
  return (
    <div className="pager">
      <span className="cell-sub">共 {total.toLocaleString('en-US')} 条</span>
      <div className="pg-btns">
        <button className="pg" disabled={page <= 1} onClick={() => onPage(page - 1)}><Icon n="left" /></button>
        {pageList(page, pages).map((p, i) =>
          p === '…'
            ? <span key={`d${i}`} className="pg-dots">…</span>
            : <button key={p} className={`pg${p === page ? ' on' : ''}`} onClick={() => onPage(p)}>{p}</button>,
        )}
        <button className="pg" disabled={page >= pages} onClick={() => onPage(page + 1)}><Icon n="right" /></button>
      </div>
    </div>
  );
}

/* ---------------- 空态 / 骨架 / 提示 ---------------- */
export function Empty({ icon = 'box', title, children }: { icon?: string; title: ReactNode; children?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-ico"><Icon n={icon} /></div>
      <h3>{title}</h3>
      {children ? <p>{children}</p> : null}
    </div>
  );
}

export function Sk({ w, h = 12 }: { w?: number | string; h?: number }) {
  return <span className="sk" style={{ display: 'inline-block', width: w ?? '100%', height: h }} />;
}

export function SkLines({ rows = ['w80', 'w60', 'w80', 'w60', 'w40'] }: { rows?: string[] }) {
  return (
    <div className="stack-sm stack">
      {rows.map((r, i) => (
        <Sk key={i} w={r.startsWith('w') ? { 'w40': '40%', 'w60': '60%', 'w80': '80%' }[r] ?? '60%' : r} />
      ))}
    </div>
  );
}

export function Note({ tone = 'info', icon, children, style }:
  { tone?: 'info' | 'warn'; icon?: string; children: ReactNode; style?: React.CSSProperties }) {
  return <div className={`note note-${tone}`} style={style}><Icon n={icon ?? (tone === 'warn' ? 'warn' : 'info')} /><div>{children}</div></div>;
}

export function AlertItem({ sev, title, text, meta, extra }:
  { sev: 'error' | 'warn' | 'info' | 'ok'; title: ReactNode; text?: ReactNode; meta?: ReactNode; extra?: ReactNode }) {
  return (
    <div className={`alert-item sev-${sev}`}>
      <div className="alert-body">
        <div className="alert-title">{title}</div>
        {text ? <div className="alert-text">{text}</div> : null}
        {meta ? <div className="alert-meta">{meta}</div> : null}
      </div>
      {extra}
    </div>
  );
}

/* ---------------- 配额量表（横向，语义阈值着色） ---------------- */
export function Meter({ label, value, max, unit, hardMax, className }:
  { label?: ReactNode; value: number; max: number; unit?: string; hardMax?: number; className?: string }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  const cls = pct >= 95 ? 'r' : pct >= 80 ? 'a' : '';
  return (
    <div className={`meter${className ? ' ' + className : ''}`}>
      {label ? <div className="meter-top"><span>{label}</span><span>{value.toLocaleString('en-US')} / {max.toLocaleString('en-US')}{unit ?? ''}</span></div> : null}
      <div className="meter-track">
        <i className={cls} style={{ width: `${pct}%` }} />
        {hardMax != null && hardMax > 0 && max > 0
          ? <span className="meter-tick hard" style={{ left: `${Math.min(100, (hardMax / max) * 100)}%` }} />
          : null}
      </div>
    </div>
  );
}

/* ---------------- 迷你趋势线（SVG，无依赖） ---------------- */
export function Spark({ data, color = 'var(--brand)', h = 24, w = 84 }: { data: number[]; color?: string; h?: number; w?: number }) {
  if (!data.length) return <svg className="spark" width={w} height={h} />;
  const max = Math.max(...data, 1), min = Math.min(...data, 0);
  const span = max - min || 1;
  const pts = data.map((v, i) => `${(i / (data.length - 1 || 1)) * w},${h - 2 - ((v - min) / span) * (h - 4)}`);
  return (
    <svg className="spark" width={w} height={h} viewBox={`0 0 ${w} ${h}`} style={{ display: 'block' }}>
      <polyline points={pts.join(' ')} fill="none" stroke={color} strokeWidth="1.4" strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}

/* ---------------- 可复制文本（复制图标常驻） ---------------- */
export function Copyable({ text, children, label }:
  { text: string; children?: ReactNode; label?: string }) {
  const [done, setDone] = useState(false);
  const timer = useRef<number | null>(null);
  useEffect(() => () => { if (timer.current) window.clearTimeout(timer.current); }, []);
  return (
    <span
      className="copyable"
      title={label ?? '点击复制'}
      onClick={async e => {
        e.stopPropagation();
        const ok = await copyText(text);
        if (ok) { setDone(true); if (timer.current) window.clearTimeout(timer.current); timer.current = window.setTimeout(() => setDone(false), 1200); }
        else toast.err('复制失败，请手动选择文本');
      }}
    >
      {children ?? text}
      <Icon n={done ? 'check' : 'copy'} style={done ? { color: 'var(--ok)' } : undefined} />
    </span>
  );
}

/* ---------------- 页头 ---------------- */
export function PageHead({ title, sub, actions }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="page-head">
      <div>
        <h1>{title}</h1>
        {sub ? <div className="sub">{sub}</div> : null}
      </div>
      {actions ? <div className="head-actions">{actions}</div> : null}
    </div>
  );
}

/* ---------------- 状态条（运行总览顶部语义条） ---------------- */
export function Strip({ tone = 'aux', title, desc, extra }:
  { tone?: 'ok' | 'warn' | 'err' | 'aux'; title: ReactNode; desc?: ReactNode; extra?: ReactNode }) {
  return (
    <div className={`strip tone-${tone}`}>
      <span className="st-title">{title}</span>
      {desc ? <span className="st-desc">{desc}</span> : null}
      {extra ? <span style={{ marginLeft: 'auto' }}>{extra}</span> : null}
    </div>
  );
}

/* ---------------- 覆盖层再导出（部分调用方习惯从本文件一次导入） ---------------- */
export { Modal, Drawer, confirm, Dropdown } from './overlay';
export type { MenuItem } from './overlay';

/** 语义 tone（format.ts）→ 徽章 tone（本文件） */
export function badgeTone(t: 'ok' | 'warn' | 'err' | 'aux'): Tone {
  return t === 'ok' ? 'green' : t === 'warn' ? 'amber' : t === 'err' ? 'red' : 'slate';
}
