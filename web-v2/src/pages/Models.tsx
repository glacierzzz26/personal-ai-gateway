import { useEffect, useMemo, useState } from 'react';
import {
  App, Button, Form, Input, InputNumber, Modal, Segmented, Select, Switch, Table,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import ModelCard, { bestPrice } from '@/components/models/ModelCard';
import ModelDrawer from '@/components/models/ModelDrawer';
import CompareModal from '@/components/models/CompareModal';
import { EmptyState, ErrorState, NoResultState } from '@/components/States';
import { api } from '@/services/api';
import { capabilities, providers } from '@/constants';
import { CAP_LABEL, fmt } from '@/utils/format';
import type { Capability, Channel, ModelCatalogItem, ModelDraft } from '@/types';

type SortKey = 'price' | 'latency' | 'hot' | 'ctx';
/** 启用状态筛选:all / 仅已启用 / 仅未启用 */
type EnableState = 'all' | 'on' | 'off';

/** 真实可用性:目录启用 且有至少一个启用供给源(与选路引擎口径一致)。 */
const usable = (m: ModelCatalogItem): boolean => m.enabled && m.offers.some(o => o.enabled);

/** 「新增模型」表单值 */
interface ModelFormValues {
  name: string;
  contextWindow?: number;
  capabilities?: Capability[];
  enabled?: boolean;
}

/** 默认筛选条件,供空结果态「清除筛选」复位。 */
const NO_FILTER = { kw: '', provider: '', cap: '', ctxRange: '' };

function AddModelModal({ open, onClose, onCreate, creating }: {
  open: boolean;
  onClose: () => void;
  onCreate: (v: ModelFormValues) => Promise<void>;
  creating: boolean;
}) {
  const [form] = Form.useForm<ModelFormValues>();
  return (
    <Modal
      open={open}
      title="新增模型"
      onCancel={onClose}
      destroyOnHidden
      width={480}
      footer={
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <Button onClick={onClose}>取消</Button>
          <Button
            type="primary"
            loading={creating}
            onClick={async () => {
              try {
                const v = await form.validateFields();
                await onCreate(v);
              } catch {
                /* 校验未过或请求失败：保持弹窗以便修正 */
              }
            }}
          >
            创建
          </Button>
        </div>
      }
    >
      <Form
        form={form}
        layout="vertical"
        requiredMark={false}
        initialValues={{ contextWindow: 0, capabilities: [], enabled: true }}
      >
        <Form.Item
          name="name"
          label="模型名称"
          rules={[{ required: true, whitespace: true, message: '请输入模型名称' }]}
        >
          <Input placeholder="如 gpt-4o-mini" className="gw-mono" />
        </Form.Item>
        <Form.Item name="contextWindow" label="上下文窗口">
          <InputNumber min={0} step={1000} style={{ width: '100%' }} addonAfter="tokens" />
        </Form.Item>
        <Form.Item name="capabilities" label="能力">
          <Select
            mode="multiple"
            allowClear
            placeholder="选择能力标签，可留空"
            options={capabilities.map(x => ({ value: x, label: CAP_LABEL[x] }))}
          />
        </Form.Item>
        <Form.Item name="enabled" label="启用" valuePropName="checked">
          <Switch />
        </Form.Item>
      </Form>
    </Modal>
  );
}

/** 从渠道同步模型：先选渠道再拉取 */
function SyncModal({ open, onClose, channels, onSync, syncing }: {
  open: boolean;
  onClose: () => void;
  channels: Channel[];
  onSync: (channelId: number) => Promise<void>;
  syncing: boolean;
}) {
  const { message } = App.useApp();
  const [cid, setCid] = useState<number | undefined>(undefined);

  useEffect(() => {
    if (open) setCid(undefined);
  }, [open]);

  const run = async () => {
    if (!cid) {
      message.warning('请先选择要同步的渠道');
      return;
    }
    try {
      await onSync(cid);
    } catch {
      /* 失败提示由父级 mutation 处理 */
    }
  };

  return (
    <Modal
      open={open}
      title="从渠道同步模型"
      onCancel={onClose}
      destroyOnHidden
      width={480}
      footer={
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <Button onClick={onClose}>取消</Button>
          <Button type="primary" loading={syncing} disabled={channels.length === 0} onClick={run}>
            开始同步
          </Button>
        </div>
      }
    >
      <div className="gw-note" role="status" style={{ marginBottom: 16 }}>
        <b>拉取上游模型清单</b>
        <span>
          网关将调用该渠道上游 <span className="gw-mono">/v1/models</span>：目录中不存在的模型会自动新建，已存在的自动补一条停用供给源（定价后启用）。
        </span>
      </div>
      {channels.length === 0 ? (
        <EmptyState title="还没有渠道" desc="请先在「渠道管理」创建一条渠道，才能从它同步模型。" />
      ) : (
        <Select
          style={{ width: '100%' }}
          placeholder="选择渠道"
          value={cid}
          onChange={setCid}
          showSearch
          optionFilterProp="label"
          autoFocus
          options={channels.map(ch => ({ value: ch.id, label: `${ch.name}（${ch.provider}）` }))}
        />
      )}
    </Modal>
  );
}

export default function Models() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();

  const [kw, setKw] = useState(NO_FILTER.kw);
  const [provider, setProvider] = useState(NO_FILTER.provider);
  const [cap, setCap] = useState(NO_FILTER.cap);
  const [ctxRange, setCtxRange] = useState(NO_FILTER.ctxRange);
  const [sort, setSort] = useState<SortKey>('price');
  const [enableState, setEnableState] = useState<EnableState>('on');
  const [view, setView] = useState<string>('plaza');
  const [compare, setCompare] = useState<number[]>([]);
  const [drawerId, setDrawerId] = useState<number | null>(null);
  const [compareOpen, setCompareOpen] = useState(false);
  const [syncOpen, setSyncOpen] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [busyId, setBusyId] = useState<number | null>(null);

  const { data: models = [], isLoading, isError, refetch } = useQuery({ queryKey: ['models'], queryFn: api.getModels });
  const { data: channels = [] } = useQuery({ queryKey: ['channels'], queryFn: api.getChannels });

  const toggleModel = useMutation({
    mutationFn: (v: { id: number; enabled: boolean }) => {
      setBusyId(v.id);
      return api.toggleModel(v.id, v.enabled);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['models'] }),
    onSettled: () => setBusyId(null),
    onError: () => message.error('启停失败，请稍后重试'),
  });

  const createModel = useMutation({
    mutationFn: (d: ModelDraft) => api.createModel(d),
    onSuccess: () => {
      message.success('模型已创建');
      setAddOpen(false);
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onError: (e: Error) => message.error(e.message || '创建失败'),
  });

  const syncModel = useMutation({
    mutationFn: (id: number) => api.syncModels(id),
    onSuccess: r => {
      message.success(`同步完成：新增 ${r.added} 个，更新 ${r.updated} 个，共 ${r.models.length} 个模型`);
      setSyncOpen(false);
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['channels'] });
    },
    onError: (e: Error) => message.error(e.message || '同步失败'),
  });

  const deleteModel = useMutation({
    mutationFn: (id: number) => api.deleteModel(id),
    onSuccess: (_res, id) => {
      message.success('模型已删除');
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['channels'] });
      if (drawerId === id) setDrawerId(null);
    },
    onError: (e: Error) => message.error(e.message || '删除失败'),
  });

  const handleCreateModel = async (v: ModelFormValues) => {
    await createModel.mutateAsync({
      name: v.name.trim(),
      contextWindow: Number(v.contextWindow) || 0,
      capabilities: v.capabilities ?? [],
      enabled: v.enabled !== false,
    });
  };

  const handleSyncModel = async (id: number) => {
    await syncModel.mutateAsync(id);
  };

  const confirmDeleteModel = (m: ModelCatalogItem) => {
    modal.confirm({
      title: `删除模型「${m.name}」`,
      content: '该模型下的全部供给源会一并删除，此操作不可恢复。',
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: () => deleteModel.mutateAsync(m.id).catch(() => undefined),
    });
  };

  const list = useMemo(() => {
    const filtered = models.filter(m => {
      if (kw) {
        const q = kw.toLowerCase();
        if (!m.name.toLowerCase().includes(q) && !m.originalName.toLowerCase().includes(q)) return false;
      }
      if (provider && !m.offers.some(o => o.provider === provider)) return false;
      if (cap && !m.capabilities.includes(cap as Capability)) return false;
      if (ctxRange === 's' && m.contextWindow >= 32000) return false;
      if (ctxRange === 'm' && (m.contextWindow < 32000 || m.contextWindow > 128000)) return false;
      if (ctxRange === 'l' && m.contextWindow <= 128000) return false;
      // 启用状态按“真实可用性”筛:目录启用 + 至少一个启用供给源
      if (enableState === 'on' && !usable(m)) return false;
      if (enableState === 'off' && usable(m)) return false;
      return true;
    });
    return [...filtered].sort((a, b) => {
      if (sort === 'price') return (bestPrice(a)?.inP ?? 9e9) - (bestPrice(b)?.inP ?? 9e9);
      if (sort === 'latency') {
        return Math.min(...a.offers.map(o => o.latencyMs)) - Math.min(...b.offers.map(o => o.latencyMs));
      }
      if (sort === 'hot') return b.todayRequests - a.todayRequests;
      return b.contextWindow - a.contextWindow;
    });
  }, [models, kw, provider, cap, ctxRange, sort, enableState]);

  const toggleCompare = (id: number) => {
    setCompare(prev => {
      if (prev.includes(id)) return prev.filter(x => x !== id);
      if (prev.length >= 4) {
        message.warning('最多同时对比 4 个模型');
        return prev;
      }
      return [...prev, id];
    });
  };

  const compareModels = models.filter(m => compare.includes(m.id));

  const clearFilters = () => {
    setKw(NO_FILTER.kw);
    setProvider(NO_FILTER.provider);
    setCap(NO_FILTER.cap);
    setCtxRange(NO_FILTER.ctxRange);
    setEnableState('all');
  };

  const filtering =
    !!(kw || provider || cap || ctxRange) || enableState !== 'all';

  const tableCols: ColumnsType<ModelCatalogItem> = [
    {
      title: '模型', dataIndex: 'name',
      render: (v, m) => (
        <span style={{ display: 'flex', flexDirection: 'column' }}>
          <span className="gw-mono" style={{ color: 'var(--gw-text)' }}>{v}</span>
          {m.displayName && (
            <span className="gw-mono" style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>原始名 {m.originalName}</span>
          )}
        </span>
      ),
    },
    {
      title: '供给源', key: 'offers', align: 'right', width: 110,
      render: (_, m) => (
        <span className="gw-num">
          {m.offers.filter(o => o.enabled).length}<span style={{ color: 'var(--gw-text-3)' }}>/{m.offers.length}</span>
        </span>
      ),
    },
    { title: '上下文', dataIndex: 'contextWindow', align: 'right', width: 110, render: v => <span className="gw-num">{fmt.ctx(v)}</span> },
    {
      title: '最低输入价', key: 'inP', align: 'right', width: 130,
      render: (_, m) => {
        const p = bestPrice(m);
        return <span className="gw-num" style={{ color: p ? 'var(--gw-primary)' : undefined }}>{p ? fmt.price(p.inP) : '—'}</span>;
      },
    },
    {
      title: '最低输出价', key: 'outP', align: 'right', width: 130,
      render: (_, m) => {
        const p = bestPrice(m);
        return <span className="gw-num">{p ? fmt.price(p.outP) : '—'}</span>;
      },
    },
    {
      title: '能力', dataIndex: 'capabilities', width: 220,
      render: v => (v.length ? v.map((x: Capability) => CAP_LABEL[x]).join(' · ') : '—'),
    },
    { title: '今日调用', dataIndex: 'todayRequests', align: 'right', width: 120, render: v => <span className="gw-num">{fmt.k(v)}</span> },
    {
      title: '启用', dataIndex: 'enabled', align: 'center', width: 90,
      render: (v, m) => (
        <span onClick={e => e.stopPropagation()}>
          <Switch
            size="small"
            checked={v}
            loading={busyId === m.id}
            aria-label={`${v ? '停用' : '启用'}模型 ${m.name}`}
            onChange={next => toggleModel.mutate({ id: m.id, enabled: next })}
          />
        </span>
      ),
    },
  ];

  /** 空态三选一：接口错误 / 目录为空 / 筛选无结果 —— 三者不可混为一谈。 */
  const emptyNode = isError ? (
    <ErrorState
      title="模型目录加载失败"
      desc="无法读取模型目录，已有渠道与路由不受影响。"
      onRetry={() => void refetch()}
    />
  ) : models.length === 0 ? (
    <EmptyState
      title="模型目录还是空的"
      desc="可以「从渠道同步」把上游模型批量拉进来，也可以手动新增一个。"
      action={
        <Button size="small" type="primary" onClick={() => setSyncOpen(true)} disabled={channels.length === 0}>
          从渠道同步
        </Button>
      }
    />
  ) : (
    <NoResultState
      title="没有符合条件的模型"
      desc="当前筛选（关键字 / 供应商 / 能力 / 上下文 / 启用状态）没有命中。"
      action={<Button size="small" onClick={clearFilters}>清除筛选</Button>}
    />
  );

  return (
    <div className="gw-page">
      <PageHeader
        title="模型广场"
        desc="以模型为中心聚合多家供应商供给源，可对比价格与延迟、编排优先级"
        extra={
          <>
            <Button onClick={() => setSyncOpen(true)}>从渠道同步</Button>
            <Button type="primary" onClick={() => setAddOpen(true)}>新增模型</Button>
          </>
        }
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
              style={{ width: 150 }} value={provider} onChange={setProvider}
              options={[{ value: '', label: '全部供应商' }, ...providers.map(p => ({ value: p, label: p }))]}
            />
            <Select
              style={{ width: 140 }} value={cap} onChange={setCap}
              options={[{ value: '', label: '全部能力' }, ...capabilities.map(x => ({ value: x, label: CAP_LABEL[x] }))]}
            />
            <Select
              style={{ width: 150 }} value={ctxRange} onChange={setCtxRange}
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
                { value: 'price', label: '按最低价' },
                { value: 'latency', label: '按最低延迟' },
                { value: 'hot', label: '按调用量' },
                { value: 'ctx', label: '按上下文' },
              ]}
            />
            <Segmented
              size="small"
              value={enableState}
              onChange={v => setEnableState(v as EnableState)}
              options={[
                { value: 'all', label: '全部' },
                { value: 'on', label: '仅已启用' },
                { value: 'off', label: '仅未启用' },
              ]}
            />
            <span className="count">
              {filtering ? (
                <>筛选出 <b>{list.length}</b> / {models.length} 个模型</>
              ) : (
                <>共 <b>{models.length}</b> 个模型</>
              )}
            </span>
          </div>

          <div style={{ padding: '16px 20px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 11, marginBottom: 16 }}>
              <span style={{ fontSize: 13.5, color: 'var(--gw-text-3)' }}>
                「仅已启用」按真实可用性过滤：目录启用且至少一个供给源启用
              </span>
              <div style={{ marginLeft: 'auto' }}>
                <Segmented
                  size="small"
                  value={view} onChange={setView}
                  options={[{ value: 'plaza', label: '广场' }, { value: 'table', label: '表格' }]}
                />
              </div>
            </div>

            {isLoading && models.length === 0 ? (
              <div className="gw-grid-cards">
                {[0, 1, 2, 3].map(i => (
                  <div className="gw-sk-metric" key={i} style={{ height: 190 }} />
                ))}
              </div>
            ) : list.length === 0 ? (
              emptyNode
            ) : view === 'plaza' ? (
              <div className="gw-grid-cards">
                {list.map(m => (
                  <ModelCard
                    key={m.id}
                    model={m}
                    picked={compare.includes(m.id)}
                    busy={busyId === m.id}
                    onOpen={() => setDrawerId(m.id)}
                    onToggleCompare={() => toggleCompare(m.id)}
                    onToggleEnabled={next => toggleModel.mutate({ id: m.id, enabled: next })}
                    onDelete={() => confirmDeleteModel(m)}
                  />
                ))}
              </div>
            ) : (
              <Table<ModelCatalogItem>
                rowKey="id"
                size="middle"
                dataSource={list}
                columns={tableCols}
                pagination={list.length > 20 ? { pageSize: 20, showSizeChanger: false } : false}
                scroll={{ x: 1100 }}
                onRow={r => ({
                  onClick: () => setDrawerId(r.id),
                  onKeyDown: e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setDrawerId(r.id); } },
                  tabIndex: 0,
                  style: { cursor: 'pointer' },
                })}
              />
            )}
          </div>
        </BlockCard>
      </Blocks>

      {/* 对比托盘：随滚动吸底，未选中任何模型时不出现 */}
      {compare.length > 0 && (
        <div
          style={{
            position: 'sticky', bottom: 8, marginTop: 18, zIndex: 30,
            background: 'var(--gw-card)', border: '1px solid var(--gw-border)',
            borderRadius: 'var(--gw-r-popup)', padding: '10px 14px',
            display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap',
          }}
        >
          <span style={{ fontSize: 13.5, color: 'var(--gw-text-2)' }}>
            已选 <b className="gw-num">{compare.length}</b> 个（最多 4 个）
          </span>
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
            {compareModels.map(m => (
              <span className="gw-badge gw-mono" key={m.id} style={{ gap: 7 }}>
                {m.name}
                <button
                  type="button"
                  className="gw-link"
                  style={{ padding: '0 2px', lineHeight: 1 }}
                  aria-label={`从对比中移除 ${m.name}`}
                  onClick={() => toggleCompare(m.id)}
                >
                  ✕
                </button>
              </span>
            ))}
          </div>
          <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
            <Button size="small" onClick={() => setCompare([])}>清空</Button>
            <Button size="small" type="primary" onClick={() => setCompareOpen(true)}>开始对比</Button>
          </div>
        </div>
      )}

      <ModelDrawer
        model={models.find(m => m.id === drawerId) ?? null}
        onClose={() => setDrawerId(null)}
        onDeleteModel={() => {
          const m = models.find(x => x.id === drawerId);
          if (m) confirmDeleteModel(m);
        }}
      />
      <CompareModal open={compareOpen} models={compareModels} onClose={() => setCompareOpen(false)} />
      <SyncModal open={syncOpen} channels={channels} syncing={syncModel.isPending} onClose={() => setSyncOpen(false)} onSync={handleSyncModel} />
      <AddModelModal open={addOpen} creating={createModel.isPending} onClose={() => setAddOpen(false)} onCreate={handleCreateModel} />
    </div>
  );
}
