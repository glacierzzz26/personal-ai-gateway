import { useMemo, useState } from 'react';
import {
  App, Button, Input, Modal, Select, Space, Table, Tag, Tooltip,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import ProviderMark from '@/components/ProviderMark';
import { EmptyState, ErrorState, NoResultState } from '@/components/States';
import ManualPriceModal from '@/components/models/ManualPriceModal';
import { api } from '@/services/api';
import { fmt } from '@/utils/format';
import { buildOfficialIndex, modelBinding, officialFor } from '@/utils/official';
import type {
  BillingShape, CommandCodeFetchResult, ModelCatalogItem, OfficialPriceView, OpenCodeFetchResult,
  PriceSource, Provider, RefreshPricingResp,
} from '@/types';

/** 计费形态标签。分时/阶梯/折扣必须显式标注 —— 计费按请求时刻选档,展示列的是「生效默认」档。 */
const SHAPE_LABEL: Record<BillingShape, string> = {
  flat: '单一价',
  peak_offpeak: '峰谷分时',
  tiered: '阶梯计价',
  discount: '限时折扣',
};

/** 官方价来源标签(与后端 domain.PriceSource 一致)。空串 = 迁移前遗留,读作 commandcode。 */
const SOURCE_LABEL: Record<string, string> = {
  commandcode: 'command code',
  opencode: 'opencode',
};

const sourceLabel = (s?: PriceSource) => SOURCE_LABEL[s || 'commandcode'] ?? s ?? '—';
/** 来源归一:空串(遗留行)读作 commandcode。 */
const normSrc = (s?: PriceSource): PriceSource => (s || 'commandcode');

const dash = <span style={{ color: 'var(--gw-text-3)' }}>—</span>;
const curOf = (c: string) => (c === 'CNY' ? '¥' : '$');

/** 一个官方价来源的抓取结果块。CC / opencode 同形共用;opencode 另报分档/折扣 slug。
 *  失败分支显示 err(不显示空块),成功分支列出免费行/分档/折扣 —— 被剔除或改写的信息必须显式回报。 */
function SourceResult({ label, res, err }: {
  label: string;
  res?: CommandCodeFetchResult | OpenCodeFetchResult;
  err?: string;
}) {
  if (!res) {
    if (!err) return null;
    return (
      <div className="gw-note" role="alert" style={{ borderLeftColor: 'var(--gw-err)', marginBottom: 14 }}>
        <b style={{ color: 'var(--gw-err)' }}>{label} 抓取失败</b>
        <span>{err}(未改动该来源的官方价;旧数据仍在)</span>
      </div>
    );
  }
  const tieredSlugs = 'tieredSlugs' in res ? res.tieredSlugs : undefined;
  const discountSlugs = 'discountSlugs' in res ? res.discountSlugs : undefined;
  const chipRow = (title: string, items: string[] | undefined, mono = false) => (
    (items?.length ?? 0) > 0 && (
      <div style={{ marginBottom: 14 }}>
        <div style={{ fontSize: 12.5, color: 'var(--gw-text-3)', marginBottom: 6 }}>{title}</div>
        <Space size={[6, 6]} wrap>
          {items!.map(s => (
            <Tag key={s} style={{ marginInlineEnd: 0 }} className={mono ? 'gw-mono' : undefined}>{s}</Tag>
          ))}
        </Space>
      </div>
    )
  );
  return (
    <>
      <div className="gw-note" style={{ marginBottom: 14 }}>
        <b>
          已从 {label} 抓取 {res.upserted} 行官方价
          {res.removed ? `,清理下架 ${res.removed} 行` : ''}
        </b>
        <span>
          共 {res.totalRows} 行(含免费 {res.freeSkipped?.length ?? 0} 行,已跳过)。
          {' '}
          <a href={res.sourceUrl} target="_blank" rel="noreferrer">来源 ↗</a>
        </span>
      </div>

      {(res.perVendor?.length ?? 0) > 0 && (
        <div style={{ marginBottom: 14 }}>
          <div style={{ fontSize: 12.5, color: 'var(--gw-text-3)', marginBottom: 6 }}>
            本次落库按厂商分布
          </div>
          <Space size={[6, 6]} wrap>
            {res.perVendor.map(p => (
              <Tag key={p.provider} style={{ marginInlineEnd: 0 }}>
                <ProviderMark name={p.provider} />
                <span style={{ marginLeft: 4 }}>{p.provider}</span>
                <span className="gw-num" style={{ marginLeft: 4, color: 'var(--gw-text-3)' }}>{p.count}</span>
              </Tag>
            ))}
          </Space>
        </div>
      )}

      {chipRow('免费模型(无单价,未落库)', res.freeSkipped, true)}
      {chipRow('分档模型(多档已合并为一行,标量取基准档)', tieredSlugs, true)}
      {chipRow('折扣模型(<del> 划原价,取现价)', discountSlugs, true)}
    </>
  );
}

/**
 * 官方定价:集中管理厂商官网单价(抓取 / 手工录入 / 删除),并反查每行被哪些目录模型引用。
 * 官方价与供给源报价分表存放 —— 此处只维护「官方参考价」,不会改写任何渠道报价。
 */
export default function OfficialPricing() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const navigate = useNavigate();

  const [vendor, setVendor] = useState<Provider | ''>('');
  // 来源筛选('' = 全部);官方价锚点来源:commandcode / opencode。
  const [src, setSrc] = useState<PriceSource>('');
  const [kw, setKw] = useState('');
  const [manualOpen, setManualOpen] = useState(false);
  // 批量刷新结果(CC 锚点抓取 + 显式重试厂商的成败 + 本次新绑定的模型)。
  const [bulkRes, setBulkRes] = useState<RefreshPricingResp | null>(null);
  const [bulkRunning, setBulkRunning] = useState(false);

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

  const confirmDelete = (row: OfficialPriceView) => {
    const n = refsByPrice.get(row.id)?.length ?? 0;
    modal.confirm({
      title: `删除「${row.provider} / ${row.modelName}」官方价`,
      content: n > 0
        ? `有 ${n} 个模型引用该行,删除后它们将不再显示官方参考价。已应用到供给源的报价与留证不受影响。`
        : '已应用到供给源的报价与留证不受影响。',
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: () => del.mutateAsync(row.id).catch(() => undefined),
    });
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
  const runBulkRefresh = (only?: Provider[]) => {
    const retry = only && only.length > 0;
    modal.confirm({
      title: retry ? `仅重试失败的 ${only.length} 个厂商` : '重新抓取官方价(command code + opencode)',
      content: (
        <>
          从 commandcode 模型页与 opencode zen 定价页各抓一轮作为官方价锚点(同一模型两来源各存一行),并
          <b>删除该来源下已下架、页面上不再列出的模型行</b>。
          抓完会自动把能唯一对上的模型绑定到官方价(成本与售价随之重算)。
          {!retry && (
            <div style={{ marginTop: 8, color: 'var(--gw-text-3)', fontSize: 12.5 }}>
              两个来源独立成败,一方失败不影响另一方;供给源成本按其渠道类型取对应来源的官方价。
              费用倍率不在此处 —— 官方单价即锚点,「$10 买 $60」走渠道成本系数。
            </div>
          )}
        </>
      ),
      okText: '开始抓取',
      cancelText: '取消',
      onOk: async () => {
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
      },
    });
  };

  const list = useMemo(() => {
    const q = kw.trim().toLowerCase();
    return rows
      .filter(r => (!vendor || r.provider === vendor)
        && (!src || normSrc(r.source) === src)
        && (!q || r.modelName.toLowerCase().includes(q)))
      .sort((a, b) =>
        a.provider === b.provider
          ? a.modelName.localeCompare(b.modelName)
          : a.provider.localeCompare(b.provider));
  }, [rows, vendor, src, kw]);

  const cols: ColumnsType<OfficialPriceView> = [
    {
      title: '厂商', dataIndex: 'provider', width: 130,
      render: v => (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <ProviderMark name={v} /> <span>{v}</span>
        </span>
      ),
    },
    {
      title: '模型名(官方页)', dataIndex: 'modelName', ellipsis: true,
      render: v => <Tooltip title={v}><span className="gw-mono">{v}</span></Tooltip>,
    },
    {
      title: '原币价(每百万)', key: 'native', align: 'right', width: 176,
      render: (_, r) => (
        <div className="gw-num">
          {curOf(r.currency)}{r.inputPrice} / {curOf(r.currency)}{r.outputPrice}
          <div style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
            {r.currency}{r.cacheReadPrice > 0 ? ` · 缓存读 ${curOf(r.currency)}${r.cacheReadPrice}` : ''}
            {r.cacheWritePrice ? ` · 缓存写 ${curOf(r.currency)}${r.cacheWritePrice}` : ''}
          </div>
        </div>
      ),
    },
    {
      title: '计价金额(每百万)', key: 'converted', align: 'right', width: 168,
      render: (_, r) => (r.rateSet ? (
        <div className="gw-num">
          {fmt.price(r.inputPriceUsd)} / {fmt.price(r.outputPriceUsd)}
          {r.currency !== 'CNY' && r.currency !== 'USD' && (
            <div style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>原币 {r.currency}</div>
          )}
        </div>
      ) : (
        <Tooltip title={`官方原币为 ${r.currency},与当前计价币种不一致且未设汇率 —— 请到「系统设置」填写 USD/CNY 汇率`}>
          <span style={{ color: 'var(--gw-text-3)' }}>— 需设汇率</span>
        </Tooltip>
      )),
    },
    {
      title: '形态', dataIndex: 'billingShape', width: 96,
      render: (v: BillingShape) => (
        <Tooltip title={v === 'flat' ? '官方单一价' : '官方为分时/阶梯/折扣价。分时按请求时刻自动选峰/谷价;阶梯暂按首档标量计(官方阶梯数据不可信,不消费)。此处展示的是「生效默认」档'}>
          <span className="gw-badge">{SHAPE_LABEL[v]}</span>
        </Tooltip>
      ),
    },
    // 锚点来源:同一 (厂商, 模型) 可两来源并存各存一行(commandcode / opencode)。
    {
      title: '来源', key: 'anchor', width: 116,
      render: (_, r) => (
        <Tooltip title={normSrc(r.source) === 'opencode'
          ? 'opencode zen 定价页锚点(该来源渠道的供给源成本据此派生)'
          : 'command code 单页锚点(主力 / 兜底来源)'}>
          <Tag color={normSrc(r.source) === 'opencode' ? 'blue' : 'default'} style={{ marginInlineEnd: 0 }}>
            {sourceLabel(r.source)}
          </Tag>
        </Tooltip>
      ),
    },
    {
      title: '来源页', key: 'src', width: 148,
      render: (_, r) => (
        <div style={{ fontSize: 12 }}>
          <a href={r.sourceUrl} target="_blank" rel="noreferrer" className="gw-mono">官方页面 ↗</a>
          <div style={{ color: 'var(--gw-text-3)' }}>{fmt.dt(r.fetchedAt)}</div>
          {r.cacheDerived && (
            <Tooltip title="官方页面无独立缓存价列,此值为按官方规则推导">
              <span style={{ color: 'var(--gw-warn)' }}>缓存价推导</span>
            </Tooltip>
          )}
        </div>
      ),
    },
    {
      title: '已应用', dataIndex: 'appliedOfferIds', align: 'right', width: 90,
      render: v => (v.length ? <span className="gw-num">{v.length}</span> : dash),
    },
    {
      title: '引用模型', key: 'refs',
      render: (_, r) => {
        const refs = refsByPrice.get(r.id) ?? [];
        if (refs.length === 0) {
          return <span style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>暂无模型引用</span>;
        }
        const shown = refs.slice(0, 3);
        return (
          <Space size={4} wrap>
            {shown.map(m => (
              <Tag key={m.id} style={{ marginInlineEnd: 0 }}>
                <button
                  type="button"
                  className="gw-link gw-mono"
                  onClick={() => navigate('/models')}
                >
                  {m.name}
                </button>
              </Tag>
            ))}
            {refs.length > shown.length && (
              <span style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>+{refs.length - shown.length}</span>
            )}
          </Space>
        );
      },
    },
    {
      title: '', key: 'act', align: 'right', width: 70,
      render: (_, r) => (
        <Button size="small" danger type="text" onClick={() => confirmDelete(r)}>
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
      desc="点「抓取官方价」从 command code 模型页与 opencode zen 定价页各拉一轮(覆盖全部厂商);页面动态渲染、抓不到的厂商可「手工录入」。"
      action={
        <Button size="small" type="primary" loading={bulkRunning} onClick={() => runBulkRefresh()}>
          抓取官方价
        </Button>
      }
    />
  ) : (
    <NoResultState
      title="没有符合条件的官方价"
      desc="当前厂商 / 关键字没有命中。"
      action={<Button size="small" onClick={() => { setVendor(''); setKw(''); }}>清除筛选</Button>}
    />
  );

  return (
    <div className="gw-page">
      <PageHeader
        title="官方定价"
        desc="官方参考价锚点来自 command code 与 opencode 两个来源(同一模型可两来源并存各存一行);抓取/手工录入只写官方价,不改动渠道报价"
        extra={
          <>
            <Button onClick={() => setManualOpen(true)}>手工录入</Button>
            <Tooltip title="从 command code 模型页与 opencode zen 定价页各抓一轮并回填模型绑定 —— 供给源成本按「官方价 × 渠道系数」随之重算">
              <Button type="primary" loading={bulkRunning} onClick={() => runBulkRefresh()}>
                抓取官方价
              </Button>
            </Tooltip>
          </>
        }
      />

      <Blocks>
        <BlockCard>
          <div className="gw-toolbar">
            <Select
              allowClear
              style={{ width: 200 }}
              placeholder="全部厂商"
              value={vendor || undefined}
              onChange={v => setVendor((v as Provider) ?? '')}
              options={vendors.map(v => ({ value: v.provider, label: v.provider }))}
            />
            <Select
              allowClear
              style={{ width: 168 }}
              placeholder="全部来源"
              value={src || undefined}
              onChange={v => setSrc((v as PriceSource) ?? '')}
              options={[
                { value: 'commandcode', label: 'command code' },
                { value: 'opencode', label: 'opencode' },
              ]}
            />
            <Input.Search
              allowClear
              placeholder="搜索官方模型名"
              style={{ width: 220 }}
              value={kw}
              onChange={e => setKw(e.target.value)}
            />
            {vendorInfo && (
              <span style={{ fontSize: 12.5, color: 'var(--gw-text-3)' }}>
                官网来源(手工录入时留证):
                <a href={vendorInfo.sourceUrl} target="_blank" rel="noreferrer" className="gw-mono">{vendorInfo.sourceUrl}</a>
              </span>
            )}
            <span className="count">共 <b>{list.length}</b> 行官方价</span>
          </div>

          <div style={{ padding: '16px 20px' }}>
            {isLoading && rows.length === 0 ? (
              <div className="gw-sk-metric" style={{ height: 160 }} />
            ) : list.length === 0 ? (
              emptyNode
            ) : (
              <Table<OfficialPriceView>
                rowKey="id"
                size="middle"
                dataSource={list}
                columns={cols}
                tableLayout="fixed"
                pagination={list.length > 30 ? { pageSize: 30, showSizeChanger: false } : false}
              />
            )}
          </div>
        </BlockCard>
      </Blocks>

      <ManualPriceModal
        open={manualOpen}
        defaultProvider={(vendor as Provider) || undefined}
        onClose={() => setManualOpen(false)}
      />

      {/* 重抓结果:两个官方价来源(CC / opencode)各自单列成败,逐厂商结果另列(一个失败不该淹没另一个的成功) */}
      <Modal
        title="重抓官方价结果"
        open={!!bulkRes}
        onCancel={() => setBulkRes(null)}
        width={680}
        footer={(() => {
          const failed = (bulkRes?.results ?? []).filter(r => r.error).map(r => r.provider);
          return (
            <Space>
              <Button onClick={() => setBulkRes(null)}>关闭</Button>
              {failed.length > 0 && (
                <Button type="primary" onClick={() => { setBulkRes(null); runBulkRefresh(failed); }}>
                  仅重试失败的 {failed.length} 个
                </Button>
              )}
            </Space>
          );
        })()}
      >
        {bulkRes && (
          <>
            {/* 全局口径说明(两来源共用):成本重算与绑定回填 */}
            <div style={{ marginBottom: 14, fontSize: 13, color: 'var(--gw-text-2)' }}>
              成本按「官方价 × 渠道系数」自动重算;本次新绑定 {bulkRes.bound.length} 个模型,
              未绑定的仍走手填兜底或显示「未知」。
            </div>

            {/* —— 两个官方价来源:各自独立成败,一个失败不淹没另一个 —— */}
            <SourceResult label="command code" res={bulkRes.commandCode} err={bulkRes.commandCodeError} />
            <SourceResult label="opencode" res={bulkRes.openCode} err={bulkRes.openCodeError} />

            {/* 显式重试的逐厂商结果(常规路径下为空) */}
            {bulkRes.results.length > 0 && (
              <div style={{ marginBottom: 4 }}>
                <div style={{ fontSize: 12.5, color: 'var(--gw-text-3)', marginBottom: 6 }}>
                  逐厂商结果
                </div>
                {bulkRes.results.map(r => (
                  <div
                    key={r.provider}
                    style={{
                      display: 'flex', alignItems: 'baseline', gap: 8, padding: '6px 0',
                      borderBottom: '1px solid var(--gw-border)', fontSize: 13,
                    }}
                  >
                    <ProviderMark name={r.provider} />
                    <b style={{ fontWeight: 500, width: 96 }}>{r.provider}</b>
                    {r.error ? (
                      <span style={{ color: 'var(--gw-err)', flex: 1 }}>
                        {r.errorType === 'manual_only'
                          ? '仅手工录入(逐厂商抓取已停用,官方价由 commandcode / opencode 锚点写入)'
                          : `失败(${r.errorType}):${r.error}`}
                      </span>
                    ) : (
                      <span style={{ color: 'var(--gw-text-2)', flex: 1 }}>
                        抓取 {r.upserted} 个模型{r.removed ? `,清理 ${r.removed} 行陈旧数据` : ''}
                      </span>
                    )}
                  </div>
                ))}
              </div>
            )}

            {bulkRes.backfillError && (
              <div className="gw-note" role="alert" style={{ borderLeftColor: 'var(--gw-err)', marginTop: 12 }}>
                <b style={{ color: 'var(--gw-err)' }}>绑定回填未完成</b>
                <span>{bulkRes.backfillError}(官方价已入库,不受影响)</span>
              </div>
            )}

            {bulkRes.bound.length > 0 && (
              <div style={{ marginTop: 14 }}>
                <div style={{ fontSize: 12.5, color: 'var(--gw-text-3)', marginBottom: 6 }}>
                  本次新绑定到官方价的模型
                </div>
                <Space size={[6, 6]} wrap>
                  {bulkRes.bound.map(b => (
                    <Tag key={b.modelId} style={{ marginInlineEnd: 0 }}>
                      <span className="gw-mono">{b.modelName}</span>
                      <span style={{ color: 'var(--gw-text-3)' }}> → {b.vendor}/{b.officialName}</span>
                    </Tag>
                  ))}
                </Space>
              </div>
            )}
          </>
        )}
      </Modal>
    </div>
  );
}
