import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import ProviderMark from '@/components/ProviderMark';
import { EmptyState, ErrorState, NoResultState } from '@/components/States';
import ManualPriceModal from '@/components/models/ManualPriceModal';
import { Badge, Button, Input, Pager, Select, Sk, Strip, Tag, TagList, Table } from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { confirm, Modal } from '@/ui/overlay';
import { message } from '@/ui/toast';
import { api } from '@/services/api';
import { fmt } from '@/utils/format';
import { buildOfficialIndex, modelBinding, officialFor } from '@/utils/official';
import type {
  BillingShape, CommandCodeFetchResult, ModelCatalogItem, OfficialPriceView,
  PlanChannelType, PlanFetchResult, PlanSourceResult, Provider, RefreshPricingResp,
} from '@/types';

/** 计费形态标签。分时/阶梯/折扣必须显式标注 —— 计费按请求时刻选档,展示列的是「生效默认」档。 */
const SHAPE_LABEL: Record<BillingShape, string> = {
  flat: '单一价',
  peak_offpeak: '峰谷分时',
  tiered: '阶梯计价',
  discount: '限时折扣',
};

/** 官方价来源显示名。2026-10 起收敛为单一来源 commandcode。 */
const SOURCE_LABEL = 'command code';

const dash = <span className="muted">—</span>;
const curOf = (c: string) => (c === 'CNY' ? '¥' : '$');

/** commandcode 单页锚点的抓取结果块。失败分支显示 err(不显示空块):
 *  被剔除或改写的信息必须显式回报。 */
function SourceResult({ label, res, err }: {
  label: string;
  res?: CommandCodeFetchResult;
  err?: string;
}) {
  if (!res) {
    if (!err) return null;
    return (
      <div style={{ marginBottom: 14 }}>
        <Strip tone="err">
          <span className="st-title">{label} 抓取失败</span>
          <span className="st-desc">{err}（未改动官方价；旧数据仍在）</span>
        </Strip>
      </div>
    );
  }
  const chipRow = (title: string, items: string[] | undefined) =>
    (items?.length ?? 0) > 0 && (
      <div style={{ marginBottom: 14 }}>
        <div className="cell-sub" style={{ marginBottom: 6 }}>{title}</div>
        <TagList>
          {items!.map(s => <Tag key={s}>{s}</Tag>)}
        </TagList>
      </div>
    );
  return (
    <>
      <div style={{ marginBottom: 14 }}>
        <Strip tone="aux">
          <span className="st-title">
            已从 {label} 抓取 {res.upserted} 行官方价
            {res.removed ? `，清理下架 ${res.removed} 行` : ''}
          </span>
          <span className="st-desc">
            共 {res.totalRows} 行（含免费 {res.freeSkipped?.length ?? 0} 行、未登记厂商 {res.unmappedSkipped?.length ?? 0} 行，已跳过）。
            {' '}
            <a href={res.sourceUrl} target="_blank" rel="noreferrer">来源 ↗</a>
          </span>
        </Strip>
      </div>

      {(res.perVendor?.length ?? 0) > 0 && (
        <div style={{ marginBottom: 14 }}>
          <div className="cell-sub" style={{ marginBottom: 6 }}>
            本次落库按厂商分布
          </div>
          <TagList>
            {res.perVendor.map(p => (
              <Tag key={p.provider}>
                <ProviderMark name={p.provider} />
                <span style={{ marginLeft: 4 }}>{p.provider}</span>
                <span className="muted" style={{ marginLeft: 4 }}>{p.count}</span>
              </Tag>
            ))}
          </TagList>
        </div>
      )}

      {chipRow('免费模型(无单价,未落库)', res.freeSkipped)}
      {chipRow('未登记厂商(已跳过,请在 ccVendorPrefixes 补映射)', res.unmappedSkipped)}
    </>
  );
}

/** 订阅计划来源显示名(CC GOAT / opencode Go,均为 $10 档)。 */
const PLAN_LABEL: Record<PlanChannelType, string> = {
  commandcode: 'command code GOAT',
  opencode: 'opencode Go',
};

/** 一个订阅计划来源的结果块。成功报「派生 N 条模型级系数 / 写入 M 渠道」,并列出跳过的免费/未映射模型。 */
function PlanResult({ src }: { src: PlanSourceResult }) {
  const label = PLAN_LABEL[src.channelType] ?? src.channelType;
  if (src.error) {
    return (
      <div style={{ marginBottom: 14 }}>
        <Strip tone="err">
          <span className="st-title">{label} 订阅计划抓取失败</span>
          <span className="st-desc">{src.error}（未改动该来源渠道的模型级系数；旧值仍在）</span>
        </Strip>
      </div>
    );
  }
  const chipRow = (title: string, items?: string[]) =>
    (items?.length ?? 0) > 0 && (
      <div style={{ marginBottom: 14 }}>
        <div className="cell-sub" style={{ marginBottom: 6 }}>{title}</div>
        <TagList>
          {items!.map(s => <Tag key={s}>{s}</Tag>)}
        </TagList>
      </div>
    );
  return (
    <>
      <div style={{ marginBottom: 14 }}>
        <Strip tone="aux">
          <span className="st-title">
            已从 {label} 订阅计划页派生 {src.written} 条模型级系数
            {src.channelsTouched > 0 ? `，写入 ${src.channelsTouched} 个渠道` : '(无匹配渠道类型,未写入)'}
          </span>
          <span className="st-desc">
            订阅价 ${src.planPriceUsd}/月 ÷ 各模型月度额度 = 系数；额度表共 {src.totalRows} 行（可派生 {src.priced} 行）。
            {' '}
            <a href={src.sourceUrl} target="_blank" rel="noreferrer">来源 ↗</a>
          </span>
        </Strip>
      </div>
      {chipRow('免费/无限额度(无法派生系数,已跳过)', src.skippedFree)}
      {chipRow('额度表未映射到官方 slug 的模型(已跳过)', src.skippedUnmapped)}
    </>
  );
}

/** 订阅计划块:逐来源列成败(CC GOAT / opencode Go 独立失败不互相淹没);整体失败时只报原因。 */
function PlanBlock({ plans, err }: { plans?: PlanFetchResult; err?: string }) {
  if (!plans) {
    if (!err) return null;
    return (
      <div style={{ marginBottom: 14 }}>
        <Strip tone="err">
          <span className="st-title">订阅计划抓取失败</span>
          <span className="st-desc">{err}（未改动任何渠道的模型级系数；旧值仍在）</span>
        </Strip>
      </div>
    );
  }
  return <>{plans.sources.map(s => <PlanResult key={s.channelType} src={s} />)}</>;
}

/**
 * 官方定价:集中管理厂商官网单价(抓取 / 手工录入 / 删除),并反查每行被哪些目录模型引用。
 * 官方价与供给源报价分表存放 —— 此处只维护「官方参考价」,不会改写任何渠道报价。
 */
export default function OfficialPricing() {
  const qc = useQueryClient();
  const navigate = useNavigate();

  const [vendor, setVendor] = useState<Provider | ''>('');
  const [kw, setKw] = useState('');
  const [page, setPage] = useState(1);
  const [manualOpen, setManualOpen] = useState(false);
  // 批量刷新结果(CC 锚点抓取 + 显式重试厂商的成败 + 本次新绑定的模型)。
  const [bulkRes, setBulkRes] = useState<RefreshPricingResp | null>(null);
  const [bulkRunning, setBulkRunning] = useState(false);
  // 独立「从订阅计划抓取」的结果(与批量刷新的 plans 块同构,但单独入口单独展示)。
  const [planRes, setPlanRes] = useState<PlanFetchResult | null>(null);
  const [planRunning, setPlanRunning] = useState(false);

  const { data: rows = [], isLoading, isError, refetch } = useQuery({
    queryKey: ['official-prices'],
    queryFn: () => api.officialPrices(),
    retry: 0,
    staleTime: 30_000,
  });
  const { data: vendors = [] } = useQuery({
    queryKey: ['official-vendors'],
    queryFn: () => api.officialVendors(),
    retry: 0,
    staleTime: 300_000,
  });
  const { data: models = [] } = useQuery({ queryKey: ['models'], queryFn: api.getModels });

  const index = useMemo(() => buildOfficialIndex(rows), [rows]);

  /** 反查:官方价行 id → 引用它的目录模型(客户端派生,无需新接口)。 */
  const refsByPrice = useMemo(() => {
    const map = new Map<number, ModelCatalogItem[]>();
    for (const m of models) {
      const ids = new Set<number>();
      for (const o of m.offers) {
        const hit = officialFor(index, m, o);
        if (hit) ids.add(hit.id);
      }
      const bound = modelBinding(index, m);
      if (bound) ids.add(bound.id);
      for (const id of ids) {
        const arr = map.get(id);
        if (arr) arr.push(m);
        else map.set(id, [m]);
      }
    }
    return map;
  }, [models, index]);

  const vendorInfo = vendors.find(v => v.provider === vendor);

  const del = useMutation({
    mutationFn: (id: number) => api.deleteOfficialPrice(id),
    onSuccess: () => {
      message.success('官方价已删除');
      qc.invalidateQueries({ queryKey: ['official-prices'] });
    },
    onError: (e: Error) => message.error(e.message || '删除失败'),
  });

  const confirmDelete = async (row: OfficialPriceView) => {
    const n = refsByPrice.get(row.id)?.length ?? 0;
    const ok = await confirm({
      title: `删除「${row.provider} / ${row.modelName}」官方价`,
      content: n > 0
        ? `有 ${n} 个模型引用该行，删除后它们将不再显示官方参考价。已应用到供给源的报价与留证不受影响。`
        : '已应用到供给源的报价与留证不受影响。',
      okText: '删除',
      cancelText: '取消',
      danger: true,
    });
    if (ok) del.mutate(row.id);
  };

  /**
   * 重抓官方价并回填 —— issue #27 后只有一个可抓来源:commandcode 单页锚点。
   *
   * 逐厂商官网抓取已停用(全部 ManualOnly),所以这里**不再**按厂商发请求,而是固定打 CC 一处。
   * `only` 传入时是「仅重试失败的厂商」:CC 不涉及,交给后端逐厂商路径(当前会以 manual_only
   * 返回,属预期);它的存在是为了让失败重试按钮仍有落点,不是常规路径。
   *
   * 抓取有破坏性(会清掉 CC 页上已下架的行),所以先显式确认再跑。
   */
  const runBulkRefresh = async (only?: Provider[]) => {
    const retry = !!(only && only.length > 0);
    const ok = await confirm({
      title: retry ? `仅重试失败的 ${only!.length} 个厂商` : '重新抓取官方价（command code）',
      content: (
        <>
          从 commandcode 模型页抓一轮作为官方价锚点,并
          <b>删除页面上已下架、不再列出的模型行</b>。
          同时从订阅计划页(CC GOAT / opencode Go)抓 Monthly credits,按模型派生成本系数写入对应渠道。
          抓完会自动把能唯一对上的模型绑定到官方价(成本与售价随之重算)。
          {!retry && (
            <div style={{ marginTop: 8, color: 'var(--t3)', fontSize: 12.5 }}>
              官方价只有一个来源(command code 单页锚点);供给源成本据此官方价 × 渠道系数重算。
              成本系数(模型级)由订阅计划页自动派生(系数 = 订阅价 ÷ 该模型额度),可在渠道页手工覆盖。
            </div>
          )}
        </>
      ),
      okText: '开始抓取',
      cancelText: '取消',
    });
    if (!ok) return;
    setBulkRunning(true);
    try {
      const r = await api.refreshOfficialPrices(only);
      setBulkRes(r);
      qc.invalidateQueries({ queryKey: ['official-prices'] });
      qc.invalidateQueries({ queryKey: ['models'] });
    } catch (e) {
      message.error((e as Error)?.message || '重抓失败');
    } finally {
      setBulkRunning(false);
    }
  };

  /**
   * 从订阅计划页(CC GOAT / opencode Go,$10 档)抓 Monthly credits,按模型派生成本系数并写入各渠道。
   * 与「抓取官方价」分离:官方价是锚点,本动作只写「渠道 × 模型」系数(不改官方价、不改报价)。
   * 自动写入 source='plan',人工覆盖(source='manual')不受影响;两来源独立成败。
   */
  const runPlanFetch = async () => {
    const ok = await confirm({
      title: '从订阅计划页抓取成本系数',
      content: (
        <>
          从 command code GOAT 与 opencode Go 两个 $10 档订阅页各抓一轮,按
          «订阅价 ÷ 各模型月度额度» 派生<b>模型级成本系数</b>,写入对应渠道类型的所有渠道。
          <div style={{ marginTop: 8, color: 'var(--t3)', fontSize: 12.5 }}>
            已有自动值会更新;人工覆盖(source=manual)不会被覆盖。两个来源独立成败,一方失败不影响另一方。
            系数只影响成本口径,不改动任何渠道报价。
          </div>
        </>
      ),
      okText: '开始抓取',
      cancelText: '取消',
    });
    if (!ok) return;
    setPlanRunning(true);
    try {
      const r = await api.fetchPlans();
      setPlanRes(r);
      qc.invalidateQueries({ queryKey: ['channel-model-cost-ratios'] });
      qc.invalidateQueries({ queryKey: ['models'] });
    } catch (e) {
      message.error((e as Error)?.message || '抓取失败');
    } finally {
      setPlanRunning(false);
    }
  };

  const list = useMemo(() => {
    const q = kw.trim().toLowerCase();
    return rows
      .filter(r => (!vendor || r.provider === vendor)
        && (!q || r.modelName.toLowerCase().includes(q)))
      .sort((a, b) =>
        a.provider === b.provider
          ? a.modelName.localeCompare(b.modelName)
          : a.provider.localeCompare(b.provider));
  }, [rows, vendor, kw]);

  /* 客户端分页(>30 行时):原表每页 30 行;筛选变化时页码由 maxPage 夹取,不会越界。 */
  const PAGE_SIZE = 30;
  const maxPage = Math.max(1, Math.ceil(list.length / PAGE_SIZE));
  const cur = Math.min(page, maxPage);
  const pageRows = list.slice((cur - 1) * PAGE_SIZE, cur * PAGE_SIZE);

  const cols: Column<OfficialPriceView>[] = [
    {
      key: 'provider', title: '厂商', width: 130,
      render: r => (
        <span className="hstack" style={{ gap: 6 }}>
          <ProviderMark name={r.provider} /> <span>{r.provider}</span>
        </span>
      ),
    },
    {
      key: 'modelName', title: '模型名(官方页)',
      render: r => <span className="mono ellip" title={r.modelName}>{r.modelName}</span>,
    },
    {
      key: 'native', title: '原币价(每百万)', align: 'right', width: 176,
      render: r => (
        <div className="num">
          {curOf(r.currency)}{r.inputPrice} / {curOf(r.currency)}{r.outputPrice}
          <div className="cell-sub">
            {r.currency}{r.cacheReadPrice > 0 ? ` · 缓存读 ${curOf(r.currency)}${r.cacheReadPrice}` : ''}
            {r.cacheWritePrice ? ` · 缓存写 ${curOf(r.currency)}${r.cacheWritePrice}` : ''}
          </div>
        </div>
      ),
    },
    {
      key: 'converted', title: '计价金额(每百万)', align: 'right', width: 168,
      render: r => (r.rateSet ? (
        <div className="num">
          {fmt.price(r.inputPriceUsd)} / {fmt.price(r.outputPriceUsd)}
          {r.currency !== 'CNY' && r.currency !== 'USD' && (
            <div className="cell-sub">原币 {r.currency}</div>
          )}
        </div>
      ) : (
        <span className="muted" title={`官方原币为 ${r.currency}，与当前计价币种不一致且未设汇率 —— 请到「系统设置」填写 USD/CNY 汇率`}>— 需设汇率</span>
      )),
    },
    {
      key: 'billingShape', title: '形态', width: 96,
      render: r => (
        <span title={r.billingShape === 'flat' ? '官方单一价' : '官方为分时/阶梯/折扣价。分时按请求时刻自动选峰/谷价;阶梯暂按首档标量计(官方阶梯数据不可信,不消费)。此处展示的是「生效默认」档'}>
          <Badge tone="slate">{SHAPE_LABEL[r.billingShape]}</Badge>
        </span>
      ),
    },
    // 单一来源(commandcode 单页锚点),不再按来源分行。
    {
      key: 'anchor', title: '来源', width: 116,
      render: () => (
        <span title="command code 单页锚点(唯一官方价来源)">
          <Tag>{SOURCE_LABEL}</Tag>
        </span>
      ),
    },
    {
      key: 'src', title: '来源页', width: 148,
      render: r => (
        <div className="small">
          <a href={r.sourceUrl} target="_blank" rel="noreferrer" className="mono">官方页面 ↗</a>
          <div className="muted">{fmt.dt(r.fetchedAt)}</div>
          {r.cacheDerived && (
            <span className="muted" style={{ color: 'var(--warn)' }} title="官方页面无独立缓存价列,此值为按官方规则推导">缓存价推导</span>
          )}
        </div>
      ),
    },
    {
      key: 'appliedOfferIds', title: '已应用', align: 'right', width: 90,
      render: r => (r.appliedOfferIds.length ? <span className="num">{r.appliedOfferIds.length}</span> : dash),
    },
    {
      key: 'refs', title: '引用模型',
      render: r => {
        const refs = refsByPrice.get(r.id) ?? [];
        if (refs.length === 0) {
          return <span className="muted small">暂无模型引用</span>;
        }
        const shown = refs.slice(0, 3);
        return (
          <TagList>
            {shown.map(m => (
              <Tag key={m.id}>
                <button
                  type="button"
                  className="card-link mono"
                  style={{ border: 'none', background: 'none', padding: 0 }}
                  onClick={() => navigate('/models')}
                >
                  {m.name}
                </button>
              </Tag>
            ))}
            {refs.length > shown.length && (
              <span className="muted small">+{refs.length - shown.length}</span>
            )}
          </TagList>
        );
      },
    },
    {
      key: 'act', title: '', align: 'right', width: 70,
      render: r => (
        <Button size="sm" variant="danger" onClick={() => void confirmDelete(r)}>
          删除
        </Button>
      ),
    },
  ];

  const emptyNode = isError ? (
    <ErrorState
      title="官方价加载失败"
      desc="无法读取官方参考价,已有渠道报价不受影响。"
      onRetry={() => void refetch()}
    />
  ) : rows.length === 0 ? (
    <EmptyState
      title="还没有官方参考价"
      desc="点「抓取官方价」从 command code 模型页拉一轮(覆盖全部厂商);页面动态渲染、抓不到的厂商可「手工录入」。"
      action={
        <Button size="sm" variant="primary" loading={bulkRunning} onClick={() => void runBulkRefresh()}>
          抓取官方价
        </Button>
      }
    />
  ) : (
    <NoResultState
      title="没有符合条件的官方价"
      desc="当前厂商 / 关键字没有命中。"
      action={<Button size="sm" onClick={() => { setVendor(''); setKw(''); }}>清除筛选</Button>}
    />
  );

  return (
    <>
      <PageHeader
        title="官方定价"
        desc="官方参考价锚点来自 command code 单页(唯一来源);所有渠道的供给源官方价统一取此价。抓取/手工录入只写官方价,不改动渠道报价"
        extra={
          <>
            <Button
              title="从 CC GOAT / opencode Go 两个 $10 订阅页抓 Monthly credits → 按模型派生成本系数(系数 = 订阅价 ÷ 该模型额度,写入对应渠道;人工覆盖不动)"
              loading={planRunning}
              onClick={() => void runPlanFetch()}
            >
              从订阅计划抓取
            </Button>
            <Button onClick={() => setManualOpen(true)}>手工录入</Button>
            <Button
              variant="primary"
              title="从 command code 模型页抓一轮并回填模型绑定 —— 供给源成本按「官方价 × 渠道系数」随之重算"
              loading={bulkRunning}
              onClick={() => void runBulkRefresh()}
            >
              抓取官方价
            </Button>
          </>
        }
      />

      <Blocks>
        <BlockCard>
          <div className="toolbar">
            <Select
              style={{ width: 200 }}
              value={vendor}
              onChange={e => setVendor(e.target.value as Provider | '')}
              options={[
                { value: '', label: '全部厂商' },
                ...vendors.map(v => ({ value: v.provider, label: v.provider })),
              ]}
            />
            <Input
              placeholder="搜索官方模型名"
              style={{ width: 220 }}
              value={kw}
              onChange={e => { setKw(e.target.value); setPage(1); }}
            />
            {vendorInfo && (
              <span className="muted small">
                官网来源(手工录入时留证):
                <a href={vendorInfo.sourceUrl} target="_blank" rel="noreferrer" className="mono">{vendorInfo.sourceUrl}</a>
              </span>
            )}
            <span className="cell-sub">共 <b>{list.length}</b> 行官方价</span>
          </div>

          <div style={{ padding: '16px 20px' }}>
            {isLoading && rows.length === 0 ? (
              <Sk h={160} style={{ width: '100%' }} />
            ) : list.length === 0 ? (
              emptyNode
            ) : (
              <>
                <Table<OfficialPriceView>
                  columns={cols}
                  rows={pageRows}
                  rowKey={r => r.id}
                />
                {list.length > PAGE_SIZE && (
                  <Pager total={list.length} page={cur} size={PAGE_SIZE} onChange={setPage} />
                )}
              </>
            )}
          </div>
        </BlockCard>
      </Blocks>

      <ManualPriceModal
        open={manualOpen}
        defaultProvider={(vendor as Provider) || undefined}
        onClose={() => setManualOpen(false)}
      />

      {/* 重抓结果:官方价单一来源(command code),逐厂商结果另列(显式重试时才非空) */}
      {bulkRes && (
        <div className="overlay" style={{ zIndex: 70 }}>
          <Modal
            title="重抓官方价结果"
            width={680}
            onClose={() => setBulkRes(null)}
            footer={(() => {
              const failed = (bulkRes.results ?? []).filter(r => r.error).map(r => r.provider);
              return (
                <>
                  <Button onClick={() => setBulkRes(null)}>关闭</Button>
                  {failed.length > 0 && (
                    <Button variant="primary" onClick={() => { setBulkRes(null); void runBulkRefresh(failed); }}>
                      仅重试失败的 {failed.length} 个
                    </Button>
                  )}
                </>
              );
            })()}
          >
            <>
              {/* 全局口径说明(两来源共用):成本重算与绑定回填 */}
              <div style={{ marginBottom: 14, fontSize: 13, color: 'var(--t2)' }}>
                成本按「官方价 × 渠道系数」自动重算;本次新绑定 {bulkRes.bound.length} 个模型,
                未绑定的仍走手填兜底或显示「未知」。
              </div>

              {/* —— 官方价唯一来源:command code 单页锚点 —— */}
              <SourceResult label="command code" res={bulkRes.commandCode} err={bulkRes.commandCodeError} />

              {/* —— 订阅计划页派生的模型级成本系数(CC GOAT / opencode Go,$10 档) —— */}
              {(bulkRes.plans || bulkRes.plansError) && (
                <div className="cell-sub" style={{ margin: '4px 0 6px' }}>
                  订阅计划(模型级成本系数)
                </div>
              )}
              <PlanBlock plans={bulkRes.plans} err={bulkRes.plansError} />

              {/* 显式重试的逐厂商结果(常规路径下为空) */}
              {bulkRes.results.length > 0 && (
                <div style={{ marginBottom: 4 }}>
                  <div className="cell-sub" style={{ marginBottom: 6 }}>
                    逐厂商结果
                  </div>
                  {bulkRes.results.map(r => (
                    <div
                      key={r.provider}
                      style={{
                        display: 'flex', alignItems: 'baseline', gap: 8, padding: '6px 0',
                        borderBottom: '1px solid var(--line)', fontSize: 13,
                      }}
                    >
                      <ProviderMark name={r.provider} />
                      <b style={{ fontWeight: 500, width: 96 }}>{r.provider}</b>
                      {r.error ? (
                        <span style={{ color: 'var(--err)', flex: 1 }}>
                          {r.errorType === 'manual_only'
                            ? '仅手工录入(逐厂商抓取已停用,官方价由 command code 锚点写入)'
                            : `失败(${r.errorType}):${r.error}`}
                        </span>
                      ) : (
                        <span style={{ color: 'var(--t2)', flex: 1 }}>
                          抓取 {r.upserted} 个模型{r.removed ? `,清理 ${r.removed} 行陈旧数据` : ''}
                        </span>
                      )}
                    </div>
                  ))}
                </div>
              )}

              {bulkRes.backfillError && (
                <div style={{ marginTop: 12 }}>
                  <Strip tone="err">
                    <span className="st-title">绑定回填未完成</span>
                    <span className="st-desc">{bulkRes.backfillError}（官方价已入库，不受影响）</span>
                  </Strip>
                </div>
              )}

              {bulkRes.bound.length > 0 && (
                <div style={{ marginTop: 14 }}>
                  <div className="cell-sub" style={{ marginBottom: 6 }}>
                    本次新绑定到官方价的模型
                  </div>
                  <TagList>
                    {bulkRes.bound.map(b => (
                      <Tag key={b.modelId}>
                        <span className="mono">{b.modelName}</span>
                        <span className="muted"> → {b.vendor}/{b.officialName}</span>
                      </Tag>
                    ))}
                  </TagList>
                </div>
              )}
            </>
          </Modal>
        </div>
      )}

      {/* 独立「从订阅计划抓取」的结果(与批量刷新里的 plans 块同构) */}
      {planRes && (
        <div className="overlay" style={{ zIndex: 70 }}>
          <Modal
            title="订阅计划抓取结果"
            width={680}
            onClose={() => setPlanRes(null)}
            footer={<Button onClick={() => setPlanRes(null)}>关闭</Button>}
          >
            <>
              <div style={{ marginBottom: 14, fontSize: 13, color: 'var(--t2)' }}>
                已按「订阅价 ÷ 各模型月度额度」派生模型级成本系数,写入对应渠道类型的所有渠道。
                可在各渠道「成本系数」里查看,或删除以退回厂商级兜底。
              </div>
              <PlanBlock plans={planRes} />
            </>
          </Modal>
        </div>
      )}
    </>
  );
}
