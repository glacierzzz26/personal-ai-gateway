import { Card } from 'antd';
import type { ReactNode } from 'react';

/**
 * 页面区块积木。
 *
 * 全站页面内容都由垂直堆叠的「区块」组成；区块 = 可选标题栏 + 内容。
 * 用这些组件而不是各页手写 div，是为了让标题字号、内边距、边框在不同页面完全一致。
 */

/** 垂直区块容器 */
export function Blocks({ children, style }: { children: ReactNode; style?: React.CSSProperties }) {
  return (
    <div className="gw-blocks" style={style}>
      {children}
    </div>
  );
}

/** 区块外壳：等价于一张卡片 */
export function Block({
  children,
  style,
  className,
  id,
}: {
  children: ReactNode;
  style?: React.CSSProperties;
  className?: string;
  id?: string;
}) {
  return (
    <Card id={id} className={className} style={style} styles={{ body: { padding: 0 } }}>
      {children}
    </Card>
  );
}

/** 区块标题栏。sub 为辅助说明，right 放链接或轻量信息（不放本页级操作）。 */
export function BlockHead({
  title,
  sub,
  right,
}: {
  title: string;
  sub?: ReactNode;
  right?: ReactNode;
}) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 11,
        padding: '16px 20px',
        borderBottom: '1px solid var(--gw-border)',
      }}
    >
      <h3 style={{ margin: 0, fontSize: 16, fontWeight: 500, color: 'var(--gw-text)' }}>{title}</h3>
      {sub && <span style={{ fontSize: 13.5, color: 'var(--gw-text-3)' }}>{sub}</span>}
      {right && <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 11 }}>{right}</div>}
    </div>
  );
}

/** 区块正文 */
export function BlockBody({
  children,
  flush,
  style,
}: {
  children: ReactNode;
  /** flush：内容自带内边距（如表格），不再套一层 padding */
  flush?: boolean;
  style?: React.CSSProperties;
}) {
  return <div style={{ padding: flush ? 0 : '18px 20px', ...style }}>{children}</div>;
}

/** 指标块：同尺寸网格里的单个指标 */
export function MetricBlock({
  label,
  value,
  unit,
  delta,
  deltaGood,
  note,
  spark,
}: {
  label: string;
  value: string;
  unit?: string;
  delta?: string;
  /** 该变化是否为「好」——升未必好（失败率升是坏），由调用方判定 */
  deltaGood?: boolean;
  note?: string;
  spark?: ReactNode;
}) {
  return (
    <Card className="gw-card-hover" styles={{ body: { padding: 0 } }}>
      <div className="gw-metric">
        <div className="l">{label}</div>
        <div className="v">
          {value}
          {unit && <small>{unit}</small>}
        </div>
        <div className="d">
          {delta && (
            <span className={deltaGood ? 'down' : 'up'}>
              {delta.startsWith('-') ? '▼' : '▲'} {delta.replace(/^[+-]/, '')}
            </span>
          )}
          {note && <span>{note}</span>}
        </div>
        {spark}
      </div>
    </Card>
  );
}
