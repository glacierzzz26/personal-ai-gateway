import { Button, Card } from 'antd';
import type { ReactNode } from 'react';
import { IconAlert, IconInbox, IconSearch } from '@/components/icons';

/**
 * 五种状态积木：加载 / 空数据 / 空结果 / 错误 / 降级。
 *
 * 「空数据」与「空结果」是两件事，不能合并：
 * - 空数据：这个区域还没有任何东西（尚未建渠道）
 * - 空结果：有数据，但当前筛选条件没命中
 * 混在一起会让用户以为是系统没数据，而去重新建资源。
 */

/* ---------- 加载 ---------- */
export function SkLine({ w }: { w?: 'w40' | 'w60' | 'w80' }) {
  return <div className={`gw-sk-l${w ? ` ${w}` : ''}`} />;
}

export function SkLines({ rows }: { rows: Array<'w40' | 'w60' | 'w80' | ''> }) {
  return (
    <div className="gw-sk">
      {rows.map((w, i) => (
        <SkLine key={i} w={w || undefined} />
      ))}
    </div>
  );
}

export function SkBlock() {
  return <div className="gw-sk-block" />;
}

export function SkMetric() {
  return <div className="gw-sk-metric" />;
}

/* ---------- 空 / 错误 版式 ---------- */
interface EstateProps {
  title: string;
  desc?: string;
  action?: ReactNode;
  /** 错误态用警示图标 */
  isError?: boolean;
  /** 空结果用放大镜图标；空数据用收件箱 */
  isSearch?: boolean;
}

function Estate({ title, desc, action, isError, isSearch }: EstateProps) {
  const Icon = isError ? IconAlert : isSearch ? IconSearch : IconInbox;
  return (
    <div className="gw-estate">
      <span className={`ic${isError ? ' err' : ''}`}>
        <Icon size={42} />
      </span>
      <div className="et">{title}</div>
      {desc && <div className="ed">{desc}</div>}
      {action && <div className="ea">{action}</div>}
    </div>
  );
}

/** 空数据：该区域尚无任何内容 */
export function EmptyState({ title, desc, action }: Omit<EstateProps, 'isError' | 'isSearch'>) {
  return <Estate title={title} desc={desc} action={action} />;
}

/** 空结果：有数据但筛选没命中 */
export function NoResultState({ title, desc, action }: Omit<EstateProps, 'isError' | 'isSearch'>) {
  return <Estate title={title} desc={desc} action={action} isSearch />;
}

/** 错误 + 重试（重试按钮由调用方传入，逻辑留在页面） */
export function ErrorState({
  title,
  desc,
  onRetry,
  retryText = '重试',
}: {
  title: string;
  desc?: string;
  onRetry?: () => void;
  retryText?: string;
}) {
  return (
    <Estate
      title={title}
      desc={desc}
      isError
      action={onRetry ? <Button size="small" onClick={onRetry}>{retryText}</Button> : undefined}
    />
  );
}

/** 卡片内错误态：错误信息常带接口路径，用等宽体 */
export function ErrorCard({ title, desc, onRetry }: { title: string; desc?: string; onRetry?: () => void }) {
  return (
    <Card>
      <ErrorState title={title} desc={desc} onRetry={onRetry} />
    </Card>
  );
}

/** 降级横幅：功能可用但结果不完整/不新鲜时使用 */
export function DegradedNote({
  title,
  children,
  action,
}: {
  title: string;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="gw-note" role="status" style={{ borderLeftColor: 'var(--gw-warn)', background: 'var(--gw-card)' }}>
      <b style={{ color: 'var(--gw-warn)' }}>⚠ {title}</b>
      <span style={{ flex: 1 }}>{children}</span>
      {action}
    </div>
  );
}
