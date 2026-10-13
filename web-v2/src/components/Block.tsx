import type { ReactNode } from 'react';
import { Card, CardBody, CardHead, Kpi, Trend } from '@/ui/primitives';

/**
 * 页面区块积木（薄封装，底层是原型自绘卡片）。
 *
 * 全站页面内容都由垂直堆叠的「区块」组成；区块 = 可选标题栏 + 内容。
 * 用这些组件而不是各页手写 div，是为了让标题字号、内边距、边框在不同页面完全一致。
 */

/** 垂直区块容器 */
export function Blocks({ children, style }: { children: ReactNode; style?: React.CSSProperties }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14, ...style }}>{children}</div>
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
    <Card id={id} className={className} style={style}>
      {children}
    </Card>
  );
}

/** 区块标题栏。sub 为辅助说明，right 放链接或轻量信息（不放本页级操作）。 */
export function BlockHead({ title, sub, right }: { title: ReactNode; sub?: ReactNode; right?: ReactNode }) {
  return <CardHead title={title} count={sub} right={right} />;
}

/** 区块正文 */
export function BlockBody({ children, style }: { children: ReactNode; style?: React.CSSProperties }) {
  return <CardBody style={style}>{children}</CardBody>;
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
  label: ReactNode;
  value: ReactNode;
  unit?: ReactNode;
  delta?: string;
  /** 该变化是否为「好」——升未必好（失败率升是坏），由调用方判定 */
  deltaGood?: boolean;
  note?: ReactNode;
  spark?: ReactNode;
}) {
  const d =
    delta != null ? (
      <Trend dir={delta.startsWith('-') ? 'down' : 'up'} good={deltaGood}>
        {delta.replace(/^[+-]/, '')}
      </Trend>
    ) : undefined;
  return <Kpi label={label} value={value} unit={unit} note={note} delta={d} spark={spark} />;
}
