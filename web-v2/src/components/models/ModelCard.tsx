import { useState } from 'react';
import ProviderMark from '@/components/ProviderMark';
import { Button, Caps, Switch } from '@/ui/primitives';
import { Menu } from '@/ui/overlay';
import { fmt } from '@/utils/format';
import type { ModelCatalogItem, OfficialPriceView } from '@/types';

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
  /** 匹配到的官方参考价(任一供给源 provider + 模型名对上),有则显示来源角标 */
  officialPrice?: OfficialPriceView;
  onOpen: () => void;
  onToggleCompare: () => void;
  onToggleEnabled: (v: boolean) => void;
  onDelete: () => void;
  /** 合并到另一个模型(消除重复行) */
  onMerge: () => void;
}

export default function ModelCard({
  model, picked, busy, officialPrice, onOpen, onToggleCompare, onToggleEnabled, onDelete, onMerge,
}: Props) {
  const [menuOpen, setMenuOpen] = useState(false);
  const p = bestPrice(model);
  const usable = model.offers.filter(o => o.enabled).length;
  const officialBadge = officialPrice;

  // 富文本 tooltip 换原生 title —— 一行串装下官方价/来源/抓取时间。
  const officialTitle = officialBadge
    ? `${officialBadge.currency === 'CNY' ? '¥' : '$'}${officialBadge.inputPrice} / ${officialBadge.currency === 'CNY' ? '¥' : '$'}${officialBadge.outputPrice}\n来源:${officialBadge.sourceUrl}\n抓取:${fmt.dt(officialBadge.fetchedAt)}`
    : '';

  return (
    <div className="mcard" onClick={onOpen} style={{ cursor: 'pointer', height: '100%' }}>
      <div className="mc-top">
        <span style={{ minWidth: 0 }}>
          <span className="mc-name">{model.name}</span>
          {model.displayName && (
            <span className="mono" style={{ display: 'block', fontSize: 11, color: 'var(--t3)', wordBreak: 'break-all' }}>
              原始名 {model.originalName}
            </span>
          )}
        </span>
        <span className="mc-ctx">{model.offers.length} 家</span>
      </div>

      <div className="hstack" style={{ gap: 6 }}>
        {model.offers.slice(0, 3).map(o => (
          <ProviderMark key={o.id} name={o.provider} />
        ))}
        {model.offers.length > 3 && (
          <span style={{ fontSize: 12, color: 'var(--t3)' }}>+{model.offers.length - 3}</span>
        )}
        <span style={{ fontSize: 12, color: 'var(--t3)', marginLeft: 4 }}>
          {fmt.ctx(model.contextWindow)} 上下文
        </span>
      </div>

      <div>
        <Caps caps={model.capabilities} />
      </div>

      <div>
        {p ? (
          <span>
            <span className="num" style={{ fontSize: 15, fontWeight: 500, color: 'var(--brand)' }}>
              {fmt.price(p.inP)}
            </span>
            <span className="num" style={{ fontSize: 13, color: 'var(--t2)' }}> / {fmt.price(p.outP)}</span>
            <span style={{ fontSize: 11, color: 'var(--t3)', marginLeft: 4 }}>输入/输出 · 每 1M tokens</span>
            {officialBadge && (
              <a
                className="badge b-brand-o"
                href={officialBadge.sourceUrl}
                target="_blank"
                rel="noreferrer"
                style={{ marginLeft: 6, fontSize: 11 }}
                title={officialTitle}
                onClick={e => e.stopPropagation()}
              >
                官方 ↗
              </a>
            )}
          </span>
        ) : (
          <span className="muted" style={{ fontSize: 13 }}>未启用供给源</span>
        )}
      </div>

      <div className="mc-foot hstack" style={{ justifyContent: 'space-between' }} onClick={e => e.stopPropagation()}>
        <span>
          今日 <b className="num">{fmt.k(model.todayRequests)}</b> 次 · {usable} 家可用
        </span>
        <div className="hstack" style={{ gap: 4 }}>
          <button
            type="button"
            className="card-link"
            style={{ color: picked ? 'var(--brand)' : undefined }}
            onClick={onToggleCompare}
          >
            {picked ? '✓ 已选' : '对比'}
          </button>
          <Switch checked={model.enabled} onChange={onToggleEnabled} disabled={busy} />
          <span className="rel" style={{ position: 'relative', display: 'inline-flex' }}>
            <Button size="sm" icon="dots" aria-label="更多操作" onClick={() => setMenuOpen(o => !o)} />
            {menuOpen && (
              <Menu
                onClose={() => setMenuOpen(false)}
                items={[
                  { label: '合并到…', icon: 'route', onClick: onMerge },
                  { label: '删除模型', icon: 'trash', danger: true, onClick: onDelete },
                ]}
              />
            )}
          </span>
        </div>
      </div>
    </div>
  );
}
