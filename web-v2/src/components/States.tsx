import type { ReactNode } from 'react';
import { Button, Card, CardBody, Empty, Sk } from '@/ui/primitives';

/**
 * 五种状态积木：加载 / 空数据 / 空结果 / 错误 / 降级。
 *
 * 「空数据」与「空结果」是两件事，不能合并：
 * - 空数据：这个区域还没有任何东西（尚未建渠道）
 * - 空结果：有数据，但当前筛选条件没命中
 * 混在一起会让用户以为是系统没数据，而去重新建资源。
 */

/* ---------- 加载 ---------- */
const wOf = (w?: 'w40' | 'w60' | 'w80' | '') => (w === 'w40' ? '40%' : w === 'w60' ? '60%' : w === 'w80' ? '80%' : '100%');

export function SkLine({ w }: { w?: 'w40' | 'w60' | 'w80' }) {
  return <Sk w={wOf(w)} h={12} />;
}

export function SkLines({ rows }: { rows: Array<'w40' | 'w60' | 'w80' | ''> }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {rows.map((w, i) => (
        <SkLine key={i} w={w || undefined} />
      ))}
    </div>
  );
}

export function SkBlock() {
  return <Sk h={120} style={{ width: '100%' }} />;
}

export function SkMetric() {
  return (
    <div className="card kpi">
      <Sk w="50%" h={10} />
      <Sk w="72%" h={24} style={{ marginTop: 8 }} />
      <Sk w="40%" h={10} style={{ marginTop: 8 }} />
    </div>
  );
}

/* ---------- 空 / 错误 版式 ---------- */
interface EstateProps {
  title: string;
  desc?: string;
  action?: ReactNode;
}

/** 空数据：该区域尚无任何内容 */
export function EmptyState({ title, desc, action }: EstateProps) {
  return <Empty icon="box" title={title} desc={desc} action={action} />;
}

/** 空结果：有数据但筛选没命中 */
export function NoResultState({ title, desc, action }: EstateProps) {
  return <Empty icon="search" title={title} desc={desc} action={action} />;
}

/** 错误 + 重试（重试按钮由调用方传入，逻辑留在页面） */
export function ErrorState({
  title,
  desc,
  onRetry,
  retryText = '重试',
}: EstateProps & { onRetry?: () => void; retryText?: string }) {
  return (
    <Empty icon="warn" title={title} desc={desc} action={onRetry ? <Button onClick={onRetry}>{retryText}</Button> : undefined} />
  );
}

/** 卡片内错误态：错误信息常带接口路径，用等宽体 */
export function ErrorCard({ title, desc, onRetry }: { title: string; desc?: string; onRetry?: () => void }) {
  return (
    <Card>
      <CardBody>
        <ErrorState title={title} desc={desc} onRetry={onRetry} />
      </CardBody>
    </Card>
  );
}

/** 降级横幅：功能可用但结果不完整/不新鲜时使用 */
export function DegradedNote({ title, children, action }: { title: string; children: ReactNode; action?: ReactNode }) {
  return (
    <div className="strip tone-warn" role="status">
      <span className="st-title">⚠ {title}</span>
      <span className="st-desc" style={{ flex: 1 }}>
        {children}
      </span>
      {action}
    </div>
  );
}
