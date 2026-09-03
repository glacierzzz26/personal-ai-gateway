import { Button, Card, Dropdown, Switch, Tag, Typography } from 'antd';
import { MoreOutlined } from '@ant-design/icons';
import ProviderMark from '@/components/ProviderMark';
import { CAP_LABEL, fmt } from '@/utils/format';
import type { ModelCatalogItem } from '@/types';

/** 从真实供给源推导最优报价：输入价 / 输出价各自取启用供给源的最低值 */
export function bestPrice(m: ModelCatalogItem): { inP: number; outP: number } | null {
  const on = m.offers.filter(o => o.enabled);
  if (!on.length) return null;
  return {
    inP: Math.min(...on.map(o => o.inputPriceUsd)),
    outP: Math.min(...on.map(o => o.outputPriceUsd)),
  };
}

interface Props {
  model: ModelCatalogItem;
  picked: boolean;
  /** 启用开关请求进行中 */
  busy?: boolean;
  onOpen: () => void;
  onToggleCompare: () => void;
  onToggleEnabled: (v: boolean) => void;
  onDelete: () => void;
}

export default function ModelCard({
  model, picked, busy, onOpen, onToggleCompare, onToggleEnabled, onDelete,
}: Props) {
  const p = bestPrice(model);
  const usable = model.offers.filter(o => o.enabled).length;

  return (
    <Card
      className="gw-mcard"
      onClick={onOpen}
      styles={{ body: { padding: 16, display: 'flex', flexDirection: 'column', gap: 10, height: '100%' } }}
    >
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
        <span className="gw-mono" style={{ fontSize: 14, fontWeight: 500, wordBreak: 'break-all' }}>
          {model.name}
        </span>
        <span style={{ marginLeft: 'auto', fontSize: 12, color: 'var(--gw-text-3)', flex: '0 0 auto' }}>
          {model.offers.length} 家
        </span>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        {model.offers.slice(0, 3).map(o => (
          <ProviderMark key={o.id} name={o.provider} />
        ))}
        {model.offers.length > 3 && (
          <span style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>+{model.offers.length - 3}</span>
        )}
        <span style={{ fontSize: 12, color: 'var(--gw-text-3)', marginLeft: 4 }}>
          {fmt.ctx(model.contextWindow)} 上下文
        </span>
      </div>

      <div>
        {model.capabilities.length
          ? model.capabilities.map(c => <Tag key={c}>{CAP_LABEL[c]}</Tag>)
          : <span style={{ color: 'var(--gw-text-3)', fontSize: 13 }}>—</span>}
      </div>

      <div>
        {p ? (
          <span>
            <span className="gw-num" style={{ fontSize: 15, fontWeight: 500, color: 'var(--gw-primary)' }}>
              {fmt.price(p.inP)}
            </span>
            <span className="gw-num" style={{ fontSize: 13, color: 'var(--gw-text-2)' }}> / {fmt.price(p.outP)}</span>
            <span style={{ fontSize: 11, color: 'var(--gw-text-3)', marginLeft: 4 }}>输入/输出 · 每 1M tokens</span>
          </span>
        ) : (
          <span style={{ color: 'var(--gw-text-3)', fontSize: 13 }}>未启用供给源</span>
        )}
      </div>

      <div
        style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          paddingTop: 10, borderTop: '1px solid var(--gw-border-2)', marginTop: 'auto',
        }}
      >
        <span style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
          今日 <b className="gw-num">{fmt.k(model.todayRequests)}</b> 次 · {usable} 家可用
        </span>
        <div style={{ display: 'flex', alignItems: 'center', gap: 4 }} onClick={e => e.stopPropagation()}>
          <Typography.Link
            onClick={onToggleCompare}
            style={{ color: picked ? 'var(--gw-primary)' : undefined }}
          >
            {picked ? '✓ 已选' : '对比'}
          </Typography.Link>
          <Switch size="small" checked={model.enabled} loading={busy} onChange={onToggleEnabled} />
          <Dropdown
            trigger={['click']}
            menu={{
              items: [
                { key: 'delete', label: '删除模型', danger: true, onClick: onDelete },
              ],
            }}
          >
            <Button
              type="text"
              size="small"
              style={{ padding: '0 4px', width: 22, height: 22 }}
              aria-label="更多操作"
            >
              <MoreOutlined style={{ fontSize: 13 }} />
            </Button>
          </Dropdown>
        </div>
      </div>
    </Card>
  );
}
