import { useEffect, useMemo, useState } from 'react';
import { Button, Card, Input, Select, Tooltip } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import { EmptyState, ErrorState, NoResultState } from '@/components/States';
import { api } from '@/services/api';
import { useCurrency } from '@/stores/currency';
import { capabilities } from '@/constants';
import { CAP_LABEL, fmt } from '@/utils/format';
import type { Capability, UserModelItem } from '@/types';

/*
 * 模型广场(普通用户视角)—— 只读。
 *
 * 与管理员页(Models.tsx)刻意分家:管理面能改模型/供给源/定价,用户面只回答
 * 「我能用哪些模型、每个多少钱」。数据来自 GET /models 的收敛分支
 * (server/user_models.go):无渠道名、无上游真实名、无来源 URL、无成本、无全站用量。
 * 因此这里也绝不 import 管理面的组件 —— 那些组件读 offers.channelName 之类,拿不到会炸。
 */

/** 与后端 usable 同口径:用户面只返回可用模型,故此处无 enable 筛选。 */
type SortKey = 'retail' | 'official' | 'ctx' | 'name';

/** 上下文筛选档位,与管理员页同边界(32K / 128K)。 */
const CTX = {
  s: (v: number) => v < 32000,
  m: (v: number) => v >= 32000 && v <= 128000,
  l: (v: number) => v > 128000,
} as const;
type CtxKey = '' | keyof typeof CTX;

const NO_FILTER = { kw: '', cap: '' as '' | Capability, ctx: '' as CtxKey };

/** 一行价格(划线的官方价 / 高亮的本站价)。 */
function PriceLine({ label, input, output, currency, muted }: {
  label: string;
  input?: number;
  output?: number;
  currency?: string;
  muted?: boolean;
}) {
  if (input == null || output == null) return null;
  const sym = currency === 'USD' ? '$' : '¥';
  const n = (v: number) => `${sym}${v.toFixed(v > 0 && v < 0.005 ? 4 : 2)}`;
  return (
    <span style={{ display: 'inline-flex', alignItems: 'baseline', gap: 6, fontSize: 13 }}>
      <span style={{ color: 'var(--gw-text-3)', minWidth: 52 }}>{label}</span>
      <span
        className="gw-num"
        style={{
          color: muted ? 'var(--gw-text-3)' : 'var(--gw-primary)',
          fontWeight: muted ? 400 : 500,
          fontSize: muted ? 13 : 15,
          textDecoration: muted ? 'line-through' : undefined,
        }}
      >
        {n(input)}
      </span>
      <span style={{ color: 'var(--gw-text-3)' }}>/</span>
      <span
        className="gw-num"
        style={{
          color: muted ? 'var(--gw-text-3)' : 'var(--gw-text)',
          fontWeight: muted ? 400 : 500,
          textDecoration: muted ? 'line-through' : undefined,
        }}
      >
        {n(output)}
      </span>
    </span>
  );
}

function ModelCard({ m }: { m: UserModelItem }) {
  const r = m.retail;
  const o = m.official;
  return (
    <Card
      className="gw-mcard"
      style={{ cursor: 'default' }}
      styles={{ body: { padding: 16, display: 'flex', flexDirection: 'column', gap: 11, height: '100%' } }}
    >
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
        <span className="gw-mono" style={{ fontSize: 14, fontWeight: 500, wordBreak: 'break-all', minWidth: 0 }}>
          {m.name}
        </span>
        <span style={{ marginLeft: 'auto', fontSize: 12, color: 'var(--gw-text-3)', flex: '0 0 auto' }}>
          {fmt.ctx(m.contextWindow)} 上下文
        </span>
      </div>

      <div>
        {m.capabilities.length
          ? m.capabilities.map(c => <span className="gw-badge" key={c}>{CAP_LABEL[c] ?? c}</span>)
          : <span style={{ color: 'var(--gw-text-3)', fontSize: 13 }}>—</span>}
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
        {r ? (
          <>
            <PriceLine label="本站价" input={r.input} output={r.output} currency={r.currency} />
            {o && <PriceLine label="官方价" input={o.input} output={o.output} currency={o.currency} muted />}
          </>
        ) : (
          <Tooltip title={m.priceNote}>
            <span style={{ fontSize: 13, color: 'var(--gw-warn)' }}>价格待定</span>
          </Tooltip>
        )}
      </div>

      <div style={{ fontSize: 11, color: 'var(--gw-text-3)', borderTop: '1px solid var(--gw-border)', paddingTop: 9 }}>
        输入 / 输出 · 每百万 tokens
      </div>
    </Card>
  );
}

/** 模型广场 —— 普通用户只读视图:可用模型 + 官方价/本站价两行。 */
export default function ModelsPlaza() {
  const setCurrency = useCurrency(s => s.setCurrency);
  const [kw, setKw] = useState(NO_FILTER.kw);
  const [cap, setCap] = useState<'' | Capability>(NO_FILTER.cap);
  const [ctx, setCtx] = useState<CtxKey>(NO_FILTER.ctx);
  const [sort, setSort] = useState<SortKey>('retail');

  const { data: models = [], isLoading, isError, refetch } = useQuery({
    queryKey: ['models', 'user'],
    queryFn: api.getMyModels,
  });

  // 普通用户读不到 /settings,计价币种由响应里的价格币种带回并水合(与 Me 页同规矩)。
  const cur = models.find(m => m.retail)?.retail?.currency;
  useEffect(() => {
    if (cur === 'CNY' || cur === 'USD') setCurrency(cur);
  }, [cur, setCurrency]);

  const list = useMemo(() => {
    const q = kw.trim().toLowerCase();
    const filtered = models.filter(m => {
      if (q && !m.name.toLowerCase().includes(q)) return false;
      if (cap && !m.capabilities.includes(cap)) return false;
      if (ctx && !CTX[ctx](m.contextWindow)) return false;
      return true;
    });
    const inPrice = (m: UserModelItem) => m.retail?.input ?? 9e9;
    return [...filtered].sort((a, b) => {
      if (sort === 'retail') return inPrice(a) - inPrice(b);
      if (sort === 'official') return (a.official?.input ?? 9e9) - (b.official?.input ?? 9e9);
      if (sort === 'ctx') return b.contextWindow - a.contextWindow;
      return a.name.localeCompare(b.name);
    });
  }, [models, kw, cap, ctx, sort]);

  const filtering = !!(kw || cap || ctx);
  const clearFilters = () => { setKw(''); setCap(''); setCtx(''); };

  return (
    <div className="gw-page">
      <PageHeader
        title="模型广场"
        desc="本站可用的模型与售价（输入 / 输出，每百万 tokens）"
      />

      <Blocks>
        <BlockCard>
          <div className="gw-toolbar">
            <Input.Search
              allowClear
              placeholder="搜索模型名"
              style={{ width: 240 }}
              value={kw}
              onChange={e => setKw(e.target.value)}
            />
            <Select
              style={{ width: 140 }} value={cap} onChange={setCap}
              options={[{ value: '', label: '全部能力' }, ...capabilities.map(x => ({ value: x, label: CAP_LABEL[x] ?? x }))]}
            />
            <Select
              style={{ width: 150 }} value={ctx} onChange={setCtx}
              options={[
                { value: '', label: '上下文不限' },
                { value: 's', label: '< 32K' },
                { value: 'm', label: '32K – 128K' },
                { value: 'l', label: '> 128K' },
              ]}
            />
            <Select
              style={{ width: 140 }} value={sort} onChange={setSort}
              options={[
                { value: 'retail', label: '按本站价' },
                { value: 'official', label: '按官方价' },
                { value: 'ctx', label: '按上下文' },
                { value: 'name', label: '按名称' },
              ]}
            />
            <span className="count">
              {filtering ? <>筛选出 <b>{list.length}</b> / {models.length} 个模型</> : <>共 <b>{models.length}</b> 个模型</>}
            </span>
          </div>

          <div style={{ padding: '16px 20px' }}>
            <div style={{ fontSize: 13.5, color: 'var(--gw-text-3)', marginBottom: 16 }}>
              本站价为实付口径；划线价为厂商官方挂牌价，供对比参考
            </div>

            {isLoading && models.length === 0 ? (
              <div className="gw-grid-cards">
                {[0, 1, 2, 3].map(i => <div className="gw-sk-metric" key={i} style={{ height: 190 }} />)}
              </div>
            ) : list.length === 0 ? (
              isError ? (
                <ErrorState
                  title="模型列表加载失败"
                  desc="无法读取可用模型，请稍后重试。"
                  onRetry={() => void refetch()}
                />
              ) : models.length === 0 ? (
                <EmptyState
                  title="暂无可用的模型"
                  desc="管理员尚未上架模型，或你的账号还未被授权任何模型。"
                />
              ) : (
                <NoResultState
                  title="没有符合条件的模型"
                  desc="当前筛选（关键字 / 能力 / 上下文）没有命中。"
                  action={<Button size="small" onClick={clearFilters}>清除筛选</Button>}
                />
              )
            ) : (
              <div className="gw-grid-cards">
                {list.map(m => <ModelCard key={m.name} m={m} />)}
              </div>
            )}
          </div>
        </BlockCard>
      </Blocks>
    </div>
  );
}
