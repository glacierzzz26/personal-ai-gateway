import { Btn } from '@/ui/primitives';
import { Icon } from '@/ui/icons';
import type { ReactNode } from 'react';

/**
 * 五种状态积木：加载 / 空数据 / 空结果 / 错误 / 降级。
 *
 * 「空数据」与「空结果」是两件事，不能合并：
 * - 空数据：这个区域还没有任何东西（尚未建渠道）
 * - 空结果：有数据，但当前筛选条件没命中
 * 混在一起会让用户以为是系统没数据，而去重新建资源。
 */

/* ---------- 加载 ---------- */
const W: Record<string, string> = { '': '100%', w40: '40%', w60: '60%', w80: '80%' };

export function SkLine({ w }: { w?: 'w40' | 'w60' | 'w80' }) {
  return <span className="sk" style={{ display: 'inline-block', width: w ? W[w] : '100%', height: 12 }} />;
}

export function SkLines({ rows }: { rows: Array<'w40' | 'w60' | 'w80' | ''> }) {
  return (
    <div className="stack-sm stack">
      {rows.map((w, i) => (
        <span key={i} className="sk" style={{ display: 'inline-block', width: W[w] ?? '60%', height: 12 }} />
      ))}
    </div>
  );
}

export function SkBlock() {
  return <div className="sk" style={{ display: 'block', width: '100%', height: 120, borderRadius: 5 }} />;
}

export function SkMetric() {
  return (
    <div className="card kpi">
      <div className="kpi-label"><span className="sk" style={{ display: 'inline-block', width: '50%', height: 11 }} /></div>
      <div style={{ height: 8 }} />
      <div className="kpi-value"><span className="sk" style={{ display: 'inline-block', width: '70%', height: 22 }} /></div>
    </div>
  );
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
  const ic = isError ? 'xCircle' : isSearch ? 'search' : 'box';
  return (
    <div className="empty">
      <div className="empty-ico"><Icon n={ic} /></div>
      <h3>{title}</h3>
      {desc && <p>{desc}</p>}
      {action && <div style={{ marginTop: 12 }}>{action}</div>}
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
      action={onRetry ? <Btn size="sm" onClick={onRetry}>{retryText}</Btn> : undefined}
    />
  );
}

/** 卡片内错误态：错误信息常带接口路径，用等宽体 */
export function ErrorCard({ title, desc, onRetry }: { title: string; desc?: string; onRetry?: () => void }) {
  return (
    <div className="card">
      <div className="card-body">
        <ErrorState title={title} desc={desc} onRetry={onRetry} />
      </div>
    </div>
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
    <div className="note note-warn" role="status">
      <b style={{ color: 'var(--warn)' }}>⚠ {title}</b>
      <span style={{ flex: 1 }}>{children}</span>
      {action}
    </div>
  );
}
