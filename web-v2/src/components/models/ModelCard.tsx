import ProviderMark from '@/components/ProviderMark';
import { Copyable, IconBtn, Switch } from '@/ui/primitives';
import { Dropdown } from '@/ui/overlay';
import { CAP_LABEL, fmt } from '@/utils/format';
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
  const p = bestPrice(model);
  const usable = model.offers.filter(o => o.enabled).length;
  const officialBadge = officialPrice;
  const sym = (c?: string) => (c === 'CNY' ? '¥' : '$');

  return (
    <div className="mcard" onClick={onOpen}>
      <div className="mc-top">
        <span className="mc-name">
          <Copyable text={model.name}>{model.name}</Copyable>
        </span>
        {model.displayName && (
          <span className="cell-sub mono" style={{ fontSize: 11, marginLeft: 6 }}>
            原始名 {model.originalName}
          </span>
        )}
        <span className="mc-ctx">{model.offers.length} 家</span>
      </div>

      <div className="hstack wrap" style={{ gap: 6 }}>
        {model.offers.slice(0, 3).map(o => (
          <ProviderMark key={o.id} name={o.provider} />
        ))}
        {model.offers.length > 3 && (
          <span className="cell-sub">+{model.offers.length - 3}</span>
        )}
        <span className="cell-sub" style={{ marginLeft: 4 }}>{fmt.ctx(model.contextWindow)} 上下文</span>
      </div>

      <div className="taglist">
        {model.capabilities.length
          ? model.capabilities.map(c => <span key={c} className="tag">{CAP_LABEL[c] ?? c}</span>)
          : <span className="muted small">—</span>}
      </div>

      <div className="prow">
        {p ? (
          <>
            <span className="plabel">本站价</span>
            <span className="ppair">
              <span className="pin">{fmt.price(p.inP)}</span>
              <span className="psep">/</span>
              <span className="pout">{fmt.price(p.outP)}</span>
            </span>
            {officialBadge && (
              <a
                className="badge b-brand-o"
                href={officialBadge.sourceUrl}
                target="_blank"
                rel="noreferrer"
                style={{ marginLeft: 6, fontSize: 11 }}
                title={`官方参考价 ${sym(officialBadge.currency)}${officialBadge.inputPrice} / ${sym(officialBadge.currency)}${officialBadge.outputPrice}\n来源:${officialBadge.sourceUrl}\n抓取:${fmt.dt(officialBadge.fetchedAt)}`}
                onClick={e => e.stopPropagation()}
              >
                官方 ↗
              </a>
            )}
            <span className="cell-sub" style={{ marginLeft: 4 }}>输入/输出 · 每 1M tokens</span>
          </>
        ) : (
          <span className="muted">未启用供给源</span>
        )}
      </div>

      <div className="mc-foot">
        <span>
          今日 <b className="num">{fmt.k(model.todayRequests)}</b> 次 · {usable} 家可用
        </span>
        <span
          className="hstack"
          style={{ gap: 4, marginLeft: 'auto' }}
          onClick={e => e.stopPropagation()}
        >
          <button type="button" className="link-btn" onClick={onToggleCompare}>
            {picked ? '✓ 已选' : '对比'}
          </button>
          <Switch
            checked={model.enabled}
            loading={busy}
            onChange={onToggleEnabled}
          />
          <Dropdown
            menu={[
              { key: 'merge', label: '合并到…', icon: 'layers', onClick: onMerge },
              { key: 'delete', label: '删除模型', icon: 'trash', danger: true, onClick: onDelete },
            ]}
          >
            {() => <IconBtn n="more" title="更多操作" />}
          </Dropdown>
        </span>
      </div>
    </div>
  );
}
