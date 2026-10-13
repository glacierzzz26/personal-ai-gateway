/**
 * 基础 UI 原语 —— 逐一对应原型 prototype-webv2.html 里的 helper 与自绘组件。
 * 全部无 antd；类名与原型 CSS 一致（见 styles/prototype.css）。
 */
import { useEffect, useState } from 'react';
import type {
  ButtonHTMLAttributes,
  CSSProperties,
  InputHTMLAttributes,
  ReactNode,
  SelectHTMLAttributes,
  TextareaHTMLAttributes,
} from 'react';
import clsx from 'clsx';
import { Icon } from './icons';
import type { IconName } from './icons';
import { CAP_LABEL, fmt } from '@/utils/format';
import { copyText } from '@/utils/clipboard';
import { toast } from './toast';

/* ============================ 按钮 ============================ */
type BtnVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'plain';
export function Button({
  variant = 'secondary',
  size,
  icon,
  loading,
  children,
  className,
  disabled,
  ...rest
}: {
  variant?: BtnVariant;
  size?: 'sm';
  icon?: IconName;
  loading?: boolean;
  children?: ReactNode;
  className?: string;
} & ButtonHTMLAttributes<HTMLButtonElement>) {
  const ism = size === 'sm';
  const cls = clsx('btn', variant !== 'plain' && `btn-${variant}`, ism && 'btn-sm', className);
  return (
    <button className={cls} disabled={disabled || loading} {...rest}>
      {loading ? (
        <Icon name="refresh" size={ism ? 11 : 13} className="spin" />
      ) : (
        icon && <Icon name={icon} size={ism ? 11 : 13} />
      )}
      {children}
    </button>
  );
}

/* ============================ 卡片 ============================ */
export function Card({ children, className, style, id }: { children: ReactNode; className?: string; style?: CSSProperties; id?: string }) {
  return (
    <div id={id} className={clsx('card', className)} style={style}>
      {children}
    </div>
  );
}

export function CardHead({
  title,
  icon,
  count,
  right,
}: {
  title: ReactNode;
  icon?: IconName;
  count?: ReactNode;
  right?: ReactNode;
}) {
  return (
    <div className="card-head">
      <div className="card-title">
        {icon && <Icon name={icon} size={14} />}
        {title}
        {count != null && <span className="cnt">{count}</span>}
      </div>
      {right && <div className="hstack">{right}</div>}
    </div>
  );
}

export function CardBody({ children, className, style }: { children: ReactNode; className?: string; style?: CSSProperties }) {
  return (
    <div className={clsx('card-body', className)} style={style}>
      {children}
    </div>
  );
}

/* ============================ KPI / 趋势 ============================ */
export function KpiGrid({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={clsx('kpi-grid', className)}>{children}</div>;
}

export function Kpi({
  label,
  value,
  unit,
  note,
  delta,
  spark,
  icon,
}: {
  label: ReactNode;
  value: ReactNode;
  unit?: ReactNode;
  note?: ReactNode;
  delta?: ReactNode;
  spark?: ReactNode;
  icon?: IconName;
}) {
  return (
    <div className="card kpi">
      <div className="kpi-label">
        {icon && <Icon name={icon} size={12} />}
        {label}
      </div>
      <div className="kpi-value">
        {value}
        {unit && <span className="unit">{unit}</span>}
      </div>
      {(note || delta || spark) && (
        <div className="kpi-foot">
          <div style={{ minWidth: 0 }}>
            {note && (
              <div className="cell-sub" style={{ whiteSpace: 'normal' }}>
                {note}
              </div>
            )}
            {delta}
          </div>
          {spark}
        </div>
      )}
    </div>
  );
}

/** 环比趋势。good 指「该变化是好事」——升未必好（失败率升是坏）。 */
export function Trend({ dir, good, cmp, children }: { dir: 'up' | 'down'; good?: boolean; cmp?: ReactNode; children: ReactNode }) {
  const cls = clsx('trend', dir, dir === 'up' && good === false && 'bad', dir === 'down' && good === true && 'good');
  return (
    <span className={cls}>
      <Icon name={dir === 'up' ? 'upRight' : 'downRight'} size={11} />
      {children}
      {cmp && <span className="cmp">{cmp}</span>}
    </span>
  );
}

/* ============================ 徽章 / 标签 ============================ */
type BadgeTone = 'brand' | 'brand-o' | 'green' | 'amber' | 'red' | 'slate';
const BADGE_CLS: Record<BadgeTone, string> = {
  brand: 'b-brand',
  'brand-o': 'b-brand-o',
  green: 'b-green',
  amber: 'b-amber',
  red: 'b-red',
  slate: 'b-slate',
};
export function Badge({ tone = 'slate', icon, children }: { tone?: BadgeTone; icon?: IconName; children: ReactNode }) {
  return (
    <span className={clsx('badge', BADGE_CLS[tone])}>
      {icon && <Icon name={icon} size={10} />}
      {children}
    </span>
  );
}

export function Tag({ icon, children }: { icon?: IconName; children: ReactNode }) {
  return (
    <span className="tag">
      {icon && <Icon name={icon} size={10} />}
      {children}
    </span>
  );
}
export function TagList({ children }: { children: ReactNode }) {
  return <span className="taglist">{children}</span>;
}
export function Mask({ children }: { children: ReactNode }) {
  return <span className="mask">{children}</span>;
}

/* ============================ 提示 / 状态条 ============================ */
export function Note({ tone = 'info', icon, children }: { tone?: 'info' | 'warn'; icon?: IconName; children: ReactNode }) {
  return (
    <div className={clsx('note', tone === 'warn' ? 'note-warn' : 'note-info')}>
      <Icon name={icon ?? (tone === 'warn' ? 'warn' : 'info')} size={14} />
      <div style={{ flex: 1, minWidth: 0 }}>{children}</div>
    </div>
  );
}

export function Strip({ tone = 'aux', children }: { tone?: 'ok' | 'warn' | 'err' | 'aux'; children: ReactNode }) {
  return <div className={clsx('strip', `tone-${tone}`)}>{children}</div>;
}

/* ============================ 列表条 ============================ */
export function Li({
  k,
  n,
  v,
  bar,
  barColor,
  r2,
}: {
  k: ReactNode;
  n?: ReactNode;
  v: ReactNode;
  bar?: number | null;
  barColor?: string;
  r2?: ReactNode;
}) {
  return (
    <div className="li">
      <div className="r1">
        <span className="k">{k}</span>
        {n && <span className="n">{n}</span>}
        <span className="v">{v}</span>
      </div>
      {bar != null && (
        <div className="bar">
          <i style={{ width: `${bar}%`, ...(barColor ? { background: barColor } : {}) }} />
        </div>
      )}
      {r2 && <div className="r2">{r2}</div>}
    </div>
  );
}

/* ============================ 分段 / 开关 / 勾选 ============================ */
export function Seg<T extends string | number>({
  options,
  value,
  onChange,
  size,
  className,
}: {
  options: { value: T; label: ReactNode }[];
  value: T;
  onChange: (v: T) => void;
  size?: 'sm';
  className?: string;
}) {
  return (
    <div className={clsx('seg', size === 'sm' && 'seg-sm', className)}>
      {options.map((o) => (
        <button key={String(o.value)} type="button" className={o.value === value ? 'on' : undefined} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Switch({ checked, onChange, label, disabled }: { checked: boolean; onChange?: (v: boolean) => void; label?: ReactNode; disabled?: boolean }) {
  return (
    <label className="switch">
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange?.(e.target.checked)} />
      <span className="track" />
      {label && <span className="sw-label">{label}</span>}
    </label>
  );
}

export function Checkbox({ checked, onChange, children }: { checked: boolean; onChange?: (v: boolean) => void; children?: ReactNode }) {
  return (
    <label className="cb">
      <input type="checkbox" checked={checked} onChange={(e) => onChange?.(e.target.checked)} />
      <span className="box">
        <Icon name="check" size={10} />
      </span>
      {children && <span>{children}</span>}
    </label>
  );
}

/* ============================ 表单 ============================ */
export function Field({ label, hint, error, children, className }: { label?: ReactNode; hint?: ReactNode; error?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={clsx('field', className)}>
      {label && <label>{label}</label>}
      {children}
      {error ? <span className="err-text">{error}</span> : hint ? <span className="hint">{hint}</span> : null}
    </div>
  );
}

export function Input({ invalid, className, ...rest }: InputHTMLAttributes<HTMLInputElement> & { invalid?: boolean }) {
  return <input className={clsx('input', invalid && 'err', className)} {...rest} />;
}

export function Textarea({ invalid, className, ...rest }: TextareaHTMLAttributes<HTMLTextAreaElement> & { invalid?: boolean }) {
  return <textarea className={clsx('input', invalid && 'err', className)} style={{ height: 'auto', minHeight: 84, padding: '7px 8px', lineHeight: 1.55, fontFamily: 'inherit', ...(rest.style ?? {}) }} {...rest} />;
}

export function Select({
  options,
  className,
  ...rest
}: { options: { value: string | number; label: string }[] } & SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={clsx('select', className)} {...rest}>
      {options.map((o) => (
        <option key={String(o.value)} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

/* ============================ 空态 / 骨架 ============================ */
export function Empty({ icon = 'box', title, desc, action }: { icon?: IconName; title?: ReactNode; desc?: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-ico">
        <Icon name={icon} size={19} />
      </div>
      {title && <h3>{title}</h3>}
      {desc && <p>{desc}</p>}
      {action && <div style={{ marginTop: 12 }}>{action}</div>}
    </div>
  );
}

export function Sk({ w, h, style }: { w?: number | string; h?: number | string; style?: CSSProperties }) {
  return <div className="sk" style={{ width: w, height: h, ...style }} />;
}

export function SkLines({ rows, style }: { rows: (number | string)[]; style?: CSSProperties }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8, ...style }}>
      {rows.map((w, i) => (
        <Sk key={i} w={w} h={12} />
      ))}
    </div>
  );
}

/* ============================ 分页 ============================ */
export function Pager({
  total,
  page,
  size,
  onChange,
}: {
  total: number;
  page: number;
  size: number;
  onChange: (page: number) => void;
}) {
  const pages = Math.max(1, Math.ceil(total / size));
  const cur = Math.min(Math.max(1, page), pages);
  const nums: (number | '…')[] = [];
  const push = (v: number | '…') => nums.push(v);
  if (pages <= 7) {
    for (let i = 1; i <= pages; i++) push(i);
  } else {
    push(1);
    if (cur > 3) push('…');
    for (let i = Math.max(2, cur - 1); i <= Math.min(pages - 1, cur + 1); i++) push(i);
    if (cur < pages - 2) push('…');
    push(pages);
  }
  return (
    <div className="pager">
      <span className="cell-sub">
        共 {fmt.n(total)} 条 · 每页 {size}
      </span>
      <div className="pg-btns">
        <button className="pg" disabled={cur <= 1} onClick={() => onChange(cur - 1)} aria-label="上一页">
          <Icon name="left" size={13} />
        </button>
        {nums.map((n, i) =>
          n === '…' ? (
            <span key={`e${i}`} className="pg-dots">
              …
            </span>
          ) : (
            <button key={n} className={clsx('pg', n === cur && 'on')} onClick={() => onChange(n)}>
              {n}
            </button>
          ),
        )}
        <button className="pg" disabled={cur >= pages} onClick={() => onChange(cur + 1)} aria-label="下一页">
          <Icon name="right" size={13} />
        </button>
      </div>
    </div>
  );
}

/* ============================ 表格 ============================ */
export interface Column<T> {
  key: string;
  title: ReactNode;
  width?: number | string;
  align?: 'right';
  sortable?: boolean;
  render?: (row: T, index: number) => ReactNode;
  className?: string;
}
export function Table<T>({
  columns,
  rows,
  rowKey,
  onRowClick,
  rowProps,
  sort,
  onSort,
}: {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T, index: number) => string | number;
  onRowClick?: (row: T) => void;
  /** 行级透传到 `<tr>`（拖拽排序等）；className 与下方自动类合并。 */
  rowProps?: (row: T, index: number) => React.HTMLAttributes<HTMLTableRowElement>;
  sort?: { key: string; dir: 1 | -1 };
  onSort?: (key: string) => void;
}) {
  return (
    <div className="table-wrap">
      <table className="tb">
        <thead>
          <tr>
            {columns.map((c) => {
              const active = sort?.key === c.key;
              return (
                <th
                  key={c.key}
                  className={clsx(c.align === 'right' && 'right', c.sortable && 'sortable')}
                  style={c.width ? { width: c.width } : undefined}
                  onClick={c.sortable && onSort ? () => onSort(c.key) : undefined}
                >
                  {c.sortable ? (
                    <span className={clsx('sort-ind', active && 'on')}>
                      {c.title}
                      <Icon name={active && sort?.dir === 1 ? 'up' : 'down'} size={10} />
                    </span>
                  ) : (
                    c.title
                  )}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => {
            const extra = rowProps?.(row, i);
            return (
              <tr
                {...extra}
                key={rowKey(row, i)}
                className={clsx(onRowClick && 'clickable', extra?.className)}
                onClick={onRowClick ? () => onRowClick(row) : extra?.onClick}
              >
                {columns.map((c) => (
                  <td key={c.key} className={clsx(c.align === 'right' && 'right', c.className)}>
                    {c.render ? c.render(row, i) : null}
                  </td>
                ))}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/* ============================ 价格（高峰 / 空闲两档） ============================ */
export interface PriceTriple {
  input: number;
  output: number;
  currency: string;
}
const priceStr = (n: number, cur: string) => `${cur === 'USD' ? '$' : '¥'}${n.toFixed(n > 0 && n < 0.005 ? 4 : 2)}`;

export function PricePair({ price, muted, tag }: { price?: PriceTriple | null; muted?: boolean; tag?: string }) {
  if (!price) return null;
  return (
    <span className={clsx('ppair', muted && 'muted')}>
      {tag && <span className="ptag">{tag}</span>}
      <span className="pin">{priceStr(price.input, price.currency)}</span>
      <span className="psep">/</span>
      <span className="pout">{priceStr(price.output, price.currency)}</span>
    </span>
  );
}

export function PriceRow({ label, muted, off, peak }: { label: ReactNode; muted?: boolean; off?: PriceTriple | null; peak?: PriceTriple | null }) {
  if (!off && !peak) return null;
  return (
    <div className="prow">
      <span className="plabel">{label}</span>
      {peak && <PricePair price={peak} muted={muted} tag="高峰" />}
      {off && <PricePair price={off} muted={muted} tag={peak ? '空闲' : undefined} />}
    </div>
  );
}

/* ============================ 能力 / 配额表 ============================ */
export function Caps({ caps }: { caps?: string[] | null }) {
  if (!caps || !caps.length) return <span className="muted small">—</span>;
  return (
    <span className="taglist">
      {caps.map((c) => (
        <Badge key={c} tone="slate">
          {CAP_LABEL[c] || c}
        </Badge>
      ))}
    </span>
  );
}

export function Meter({ pct, tone, label, right }: { pct: number; tone?: 'g' | 'a' | 'r'; label?: ReactNode; right?: ReactNode }) {
  const p = Math.max(0, Math.min(100, pct));
  return (
    <div className="meter">
      {(label || right) && (
        <div className="meter-top">
          <span>{label}</span>
          <span>{right}</span>
        </div>
      )}
      <div className="meter-track">
        <i className={tone} style={{ width: `${p}%` }} />
      </div>
    </div>
  );
}

export function MiniMeter({ pct, tone }: { pct: number; tone?: 'g' | 'a' | 'r' }) {
  const p = Math.max(0, Math.min(100, pct));
  return (
    <div className="mini-meter">
      <i className={tone} style={{ width: `${p}%` }} />
    </div>
  );
}

/* ============================ KV 表 ============================ */
export function KV({ pairs }: { pairs: [ReactNode, ReactNode][] }) {
  return (
    <div>
      {pairs.map(([k, v], i) => (
        <div className="kv" key={i}>
          <span className="k">{k}</span>
          <span className="v">{v}</span>
        </div>
      ))}
    </div>
  );
}

/* ============================ 可复制文本 ============================ */
export function Copyable({ text, children, title }: { text: string; children?: ReactNode; title?: string }) {
  return (
    <span
      className="copyable"
      title={title ?? `复制 ${text}`}
      onClick={(e) => {
        e.stopPropagation();
        void copyText(text).then((ok) => toast(ok ? `已复制 ${text}` : '复制失败，请手动选择', ok ? 'ok' : 'err'));
      }}
    >
      {children ?? text}
      <Icon name="copy" />
    </span>
  );
}

/* ============================ 版式辅助 ============================ */
export function Stack({ children, className, style, gap }: { children: ReactNode; className?: string; style?: CSSProperties; gap?: number | 'sm' }) {
  return (
    <div className={clsx('stack', gap === 'sm' && 'stack-sm', className)} style={style}>
      {children}
    </div>
  );
}
export function HStack({ children, className, style, gap }: { children: ReactNode; className?: string; style?: CSSProperties; gap?: number | 12 }) {
  return (
    <div className={clsx('hstack', gap === 12 && 'hstack-12', className)} style={style}>
      {children}
    </div>
  );
}

export function PageHead({ title, desc, actions }: { title: ReactNode; desc?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="page-head">
      <div style={{ minWidth: 0 }}>
        <h1>{title}</h1>
        {desc && <div className="sub">{desc}</div>}
      </div>
      {actions && <div className="head-actions">{actions}</div>}
    </div>
  );
}

export function Section({
  title,
  count,
  right,
  children,
  className,
  style,
}: {
  title?: ReactNode;
  count?: ReactNode;
  right?: ReactNode;
  children: ReactNode;
  className?: string;
  style?: CSSProperties;
}) {
  return (
    <div className={clsx('sec', className)} style={style}>
      {title && (
        <div className="sec-head">
          <div className="sec-title">
            {title}
            {count != null && <span className="cnt">{count}</span>}
          </div>
          {right}
        </div>
      )}
      {children}
    </div>
  );
}

/* ============================ Hook：受控表单字段校验 ============================ */
export function useForm<T extends Record<string, unknown>>(initial: T) {
  const [values, setValues] = useState<T>(initial);
  const [errors, setErrors] = useState<Partial<Record<keyof T, string>>>({});
  const set = <K extends keyof T>(key: K, v: T[K]) => {
    setValues((s) => ({ ...s, [key]: v }));
    setErrors((e) => (e[key] ? { ...e, [key]: undefined } : e));
  };
  return { values, setValues, errors, setErrors, set };
}

/** 首次挂载后延迟一帧再执行（用于需要布局后测量的图表重绘）。 */
export function useMounted() {
  const [m, setM] = useState(false);
  useEffect(() => {
    const t = requestAnimationFrame(() => setM(true));
    return () => cancelAnimationFrame(t);
  }, []);
  return m;
}
