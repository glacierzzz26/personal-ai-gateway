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
  BillingShape, ModelCatalogItem, OfficialPriceView, Provider, RefreshPricingResp,
} from '@/types';

/** 计费形态标签。分时/阶梯/折扣必须显式标注 —— 计费按请求时刻选档,展示列的是「生效默认」档。 */
const SHAPE_LABEL: Record<BillingShape, string> = {
  flat: '单一价',
  peak_offpeak: '峰谷分时',
  tiered: '阶梯计价',
  discount: '限时折扣',
};

const dash = <span style={{ color: 'var(--gw-text-3)' }}>—</span>;
const curOf = (c: string) => (c === 'CNY' ? '¥' : '$');

/**
 * 官方定价:集中管理厂商官网单价(抓取 / 手工录入 / 删除),并反查每行被哪些目录模型引用。
 * 官方价与供给源报价分表存放 —— 此处只维护「官方参考价」,不会改写任何渠道报价。
 */
export default function OfficialPricing() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const navigate = useNavigate();

  const [vendor, setVendor] = useState<Provider | ''>('');
  const [kw, setKw] = useState('');
  const [manualOpen, setManualOpen] = useState(false);
  const [fetching, setFetching] = useState<Provider | null>(null);
  // 批量刷新结果(逐厂商成败 + 本次新绑定的模型)。
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
  const manualOnly = !!vendorInfo?.manualOnly;

  const fetchPrices = useMutation({
    mutationFn: (p: Provider) => api.fetchOfficialPrices(p),
    onSuccess: r => {
      qc.invalidateQueries({ queryKey: ['official-prices'] });
      const failed = r.failed?.length ?? 0;
      const removed = r.removed ?? 0;
      message.success({
        content: (
          <>
            已抓取 {r.upserted} 个模型
            {failed > 0 && `,${failed} 个失败`}
            {/* 对账删除:官方页已下架的行被清掉,提示一下免得管理员以为数据丢了 */}
            {removed > 0 && `,清理陈旧行 ${removed} 条`}
            {' · '}
            <a href={r.sourceUrl} target="_blank" rel="noreferrer">官方来源 ↗</a>
          </>
        ),
        duration: removed > 0 ? 8 : 6,
      });
    },
    onError: (e: Error) => message.error(e.message || '获取失败'),
  });

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

  const runFetch = async () => {
    if (!vendor) {
      message.warning('请先选择要获取的厂商');
      return;
    }
    setFetching(vendor);
    try {
      await fetchPrices.mutateAsync(vendor);
    } catch {
      /* 失败提示由 mutation 处理 */
    } finally {
      setFetching(null);
    }
  };

  /**
   * 全部重抓并回填 —— 一个按钮覆盖所有可抓厂商。
   *
   * 抓取有破坏性(会清掉官网已下架的行),所以先显式确认再跑;逐厂商独立成败,
   * 结果弹窗按厂商分列,失败的可以只重试失败的那几个。
   */
  const runBulkRefresh = (only?: Provider[]) => {
    const targets = only ?? vendors.filter(v => !v.manualOnly).map(v => v.provider);
    if (targets.length === 0) {
      message.warning('没有可抓取的厂商');
      return;
    }
    modal.confirm({
      title: only ? '仅重试失败的厂商' : `重新抓取 ${targets.length} 个厂商的官方价`,
      content: (
        <>
          抓取会更新官方参考价,并<b>删除官网上已下架、页面上不再列出的模型行</b>。
          抓完会自动把能唯一对上的模型绑定到官方价(成本与售价随之重算)。
          <div style={{ marginTop: 8 }} className="gw-mono" >{targets.join('、')}</div>
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
          message.error((e as Error)?.message || '批量刷新失败');
        } finally {
          setBulkRunning(false);
        }
      },
    });
  };

  const list = useMemo(() => {
    const q = kw.trim().toLowerCase();
    return rows
      .filter(r => (!vendor || r.provider === vendor) && (!q || r.modelName.toLowerCase().includes(q)))
      .sort((a, b) =>
        a.provider === b.provider
          ? a.modelName.localeCompare(b.modelName)
          : a.provider.localeCompare(b.provider));
  }, [rows, vendor, kw]);

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
            {r.currency}{r.cacheReadPrice > 0 ? ` · 缓存 ${curOf(r.currency)}${r.cacheReadPrice}` : ''}
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
    {
      title: '来源', key: 'src', width: 148,
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
      desc="选择厂商后「获取官方价」自动抓取官网计费页;官方页动态渲染的厂商可「手工录入」。"
      action={
        <Button size="small" type="primary" onClick={runFetch} disabled={!vendor || manualOnly || fetchPrices.isPending}>
          获取官方价
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
        desc="厂商官网单价,作为各模型的官方参考价来源;抓取/手工录入只写官方价,不改动渠道报价"
        extra={
          <>
            <Button onClick={() => setManualOpen(true)}>手工录入</Button>
            <Tooltip title="逐个抓取全部可抓厂商,并自动回填模型绑定 —— 成本按「官方价 × 渠道系数」随之重算">
              <Button loading={bulkRunning} onClick={() => runBulkRefresh()}>
                全部重抓并回填
              </Button>
            </Tooltip>
            <Tooltip title={!vendor ? '请先选择厂商' : manualOnly ? '该厂商官方页动态渲染,只能手工录入' : ''}>
              <Button type="primary" loading={fetching !== null} disabled={!vendor || manualOnly} onClick={runFetch}>
                获取官方价
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
              options={vendors.map(v => ({
                value: v.provider,
                label: v.manualOnly ? `${v.provider}(仅手工)` : v.provider,
              }))}
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
                来源:<a href={vendorInfo.sourceUrl} target="_blank" rel="noreferrer" className="gw-mono">{vendorInfo.sourceUrl}</a>
                {manualOnly && ' · 官方页动态渲染,请手工录入'}
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

      {/* 批量刷新结果:逐厂商成败分列 —— 一个厂商失败不该淹没另外两个的成功 */}
      <Modal
        title="重抓官方价结果"
        open={!!bulkRes}
        onCancel={() => setBulkRes(null)}
        width={640}
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
            <div className="gw-note" style={{ marginBottom: 14 }}>
              <b>新抓 {bulkRes.totalUpserted} 行官方价{bulkRes.totalRemoved ? `,清理下架 ${bulkRes.totalRemoved} 行` : ''}</b>
              <span>
                成本按「官方价 × 渠道系数」自动重算。新绑定 {bulkRes.bound.length} 个模型;
                未绑定的模型成本仍走手填兜底或显示「未知」。
              </span>
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
                    失败({r.errorType}):{r.error}
                  </span>
                ) : (
                  <span style={{ color: 'var(--gw-text-2)', flex: 1 }}>
                    抓取 {r.upserted} 个模型{r.removed ? `,清理 ${r.removed} 行陈旧数据` : ''}
                  </span>
                )}
              </div>
            ))}

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
