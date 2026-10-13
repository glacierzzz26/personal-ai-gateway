import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import { EmptyState, ErrorState, NoResultState, SkBlock } from '@/components/States';
import { Button, Caps, Input, PriceRow, Select } from '@/ui/primitives';
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

function ModelCard({ m }: { m: UserModelItem }) {
  const r = m.retail;
  const o = m.official;
  return (
    <div className="mcard" style={{ cursor: 'default' }}>
      <div className="mc-top">
        <span className="mc-name">{m.name}</span>
        <span className="mc-ctx">{fmt.ctx(m.contextWindow)} 上下文</span>
      </div>

      <div>
        <Caps caps={m.capabilities} />
      </div>

      <div>
        {r ? (
          <>
            {/* 官方价在上(划线原价)、本站价在下(实付),与非分时模型的「原价→现价」读法一致。
                分时模型(官方价分档)官方/本站两行都拆「高峰 / 空闲」两档;非分时模型各自单值。
                展示面刻意不随当前时钟跳变 —— 客户截图给谁看都对得上账,
                也不会出现「按谷价看到报价、恰在峰时段请求被按峰价收费」的意外。 */}
            <PriceRow
              label="官方价"
              muted
              off={o ? { input: o.input, output: o.output, currency: o.currency } : undefined}
              peak={m.peakOfficial
                ? { input: m.peakOfficial.input, output: m.peakOfficial.output, currency: m.peakOfficial.currency }
                : undefined}
            />
            <PriceRow
              label="本站价"
              off={{ input: r.input, output: r.output, currency: r.currency }}
              peak={m.peakRetail
                ? { input: m.peakRetail.input, output: m.peakRetail.output, currency: m.peakRetail.currency }
                : undefined}
            />
            {m.peakVaries && m.peakHours && (
              <div style={{ fontSize: 11, color: 'var(--t3)' }} title={`高峰时段:${m.peakHours}。其余时间为空闲价`}>
                高峰时段 {m.peakHours.slice(0, 40)}{m.peakHours.length > 40 ? '…' : ''}
              </div>
            )}
          </>
        ) : (
          <span style={{ fontSize: 13, color: 'var(--warn)' }} title={m.priceNote}>价格待定</span>
        )}
      </div>

      <div className="mc-foot">输入 / 输出 · 每百万 tokens</div>
    </div>
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
    <>
      <PageHeader
        title="模型广场"
        desc="本站可用的模型与售价（输入 / 输出，每百万 tokens）"
      />

      <Blocks>
        <BlockCard>
          <div className="toolbar" style={{ padding: '12px 14px 0' }}>
            <div className="hstack wrap" style={{ gap: 8 }}>
              <Input
                placeholder="搜索模型名"
                style={{ width: 240 }}
                value={kw}
                onChange={e => setKw(e.target.value)}
              />
              <Select
                style={{ width: 140 }} value={cap} onChange={e => setCap(e.target.value as '' | Capability)}
                options={[{ value: '', label: '全部能力' }, ...capabilities.map(x => ({ value: x, label: CAP_LABEL[x] ?? x }))]}
              />
              <Select
                style={{ width: 150 }} value={ctx} onChange={e => setCtx(e.target.value as CtxKey)}
                options={[
                  { value: '', label: '上下文不限' },
                  { value: 's', label: '< 32K' },
                  { value: 'm', label: '32K – 128K' },
                  { value: 'l', label: '> 128K' },
                ]}
              />
              <Select
                style={{ width: 140 }} value={sort} onChange={e => setSort(e.target.value as SortKey)}
                options={[
                  { value: 'retail', label: '按本站价' },
                  { value: 'official', label: '按官方价' },
                  { value: 'ctx', label: '按上下文' },
                  { value: 'name', label: '按名称' },
                ]}
              />
            </div>
            <span className="cell-sub">
              {filtering ? <>筛选出 <b>{list.length}</b> / {models.length} 个模型</> : <>共 <b>{models.length}</b> 个模型</>}
            </span>
          </div>

          <div style={{ padding: '14px' }}>
            <div style={{ fontSize: 13, color: 'var(--t3)', marginBottom: 14 }}>
              本站价为实付口径；划线价为厂商官方挂牌价，供对比参考
            </div>

            {isLoading && models.length === 0 ? (
              <div className="mc-grid">
                {[0, 1, 2, 3].map(i => <div key={i} style={{ height: 190 }}><SkBlock /></div>)}
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
                  action={<Button size="sm" onClick={clearFilters}>清除筛选</Button>}
                />
              )
            ) : (
              <div className="mc-grid">
                {list.map(m => <ModelCard key={m.name} m={m} />)}
              </div>
            )}
          </div>
        </BlockCard>
      </Blocks>
    </>
  );
}
