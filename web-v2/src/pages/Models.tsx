import { useEffect, useMemo, useState } from 'react';
import { Btn, Copyable, Empty, Field, Input, Note, PageHead, Seg, Select, Switch, Check } from '@/ui/primitives';
import { Modal, confirm } from '@/ui/overlay';
import { toast } from '@/ui/toast';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import ModelCard, { bestPrice } from '@/components/models/ModelCard';
import ModelDrawer from '@/components/models/ModelDrawer';
import CompareModal from '@/components/models/CompareModal';
import { api } from '@/services/api';
import { capabilities, providers } from '@/constants';
import { CAP_LABEL, fmt } from '@/utils/format';
import { channelLabel } from '@/utils/channel';
import { buildOfficialIndex, officialOfModel } from '@/utils/official';
import type { Capability, Channel, ModelCatalogItem, ModelDraft, OfficialPriceView } from '@/types';

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
  const [name, setName] = useState('');
  const [contextWindow, setContextWindow] = useState('0');
  const [caps, setCaps] = useState<Capability[]>([]);
  const [enabled, setEnabled] = useState(true);

  useEffect(() => { if (open) { setName(''); setContextWindow('0'); setCaps([]); setEnabled(true); } }, [open]);

  const toggleCap = (c: Capability) =>
    setCaps(prev => prev.includes(c) ? prev.filter(x => x !== c) : [...prev, c]);

  const submit = async () => {
    if (!name.trim()) { toast.err('请输入模型名称'); return; }
    try {
      await onCreate({ name: name.trim(), contextWindow: Number(contextWindow) || 0, capabilities: caps, enabled });
    } catch { /* 失败提示由父级 mutation 处理 */ }
  };

  return (
    <Modal
      open={open}
      title="新增模型"
      width={480}
      onClose={onClose}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn variant="primary" loading={creating} onClick={submit}>创建</Btn>
        </>
      }
    >
      <Field label="模型名称">
        <Input className="mono" value={name} placeholder="如 gpt-4o-mini" onChange={e => setName(e.target.value)} />
      </Field>
      <Field label="上下文窗口">
        <Input className="mono" type="number" min="0" step="1000" value={contextWindow} onChange={e => setContextWindow(e.target.value)} />
      </Field>
      <Field label="能力">
        <div className="hstack wrap" style={{ gap: 12 }}>
          {capabilities.map(c => (
            <Check key={c} checked={caps.includes(c)} onChange={() => toggleCap(c)}>{CAP_LABEL[c]}</Check>
          ))}
        </div>
      </Field>
      <Field label="启用">
        <Switch checked={enabled} onChange={setEnabled} />
      </Field>
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
  const [cid, setCid] = useState<number | undefined>(undefined);

  useEffect(() => { if (open) setCid(undefined); }, [open]);

  const run = async () => {
    if (!cid) { toast.err('请先选择要同步的渠道'); return; }
    try { await onSync(cid); } catch { /* 失败提示由父级 mutation 处理 */ }
  };

  return (
    <Modal
      open={open}
      title="从渠道同步模型"
      width={480}
      onClose={onClose}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn variant="primary" loading={syncing} disabled={channels.length === 0} onClick={run}>开始同步</Btn>
        </>
      }
    >
      <Note>拉取上游模型清单:网关将调用该渠道上游 /v1/models,目录中不存在的模型自动新建,已存在的自动补一条停用供给源(定价后启用)。</Note>
      {channels.length === 0 ? (
        <Empty title="还没有渠道"><p>请先在「渠道管理」创建一条渠道,才能从它同步模型。</p></Empty>
      ) : (
        <Field label="选择渠道">
          <Select value={cid ?? ''} onChange={e => setCid(Number(e.target.value))}>
            <option value="">请选择渠道</option>
            {channels.map(ch => <option key={ch.id} value={ch.id}>{ch.name}（{channelLabel(ch)}）</option>)}
          </Select>
        </Field>
      )}
    </Modal>
  );
}

function MergeModal({ from, models, onClose, onConfirm }: {
  from: ModelCatalogItem | null;
  models: ModelCatalogItem[];
  onClose: () => void;
  onConfirm: (into: ModelCatalogItem) => void;
}) {
  const [intoId, setIntoId] = useState<number | undefined>(undefined);

  useEffect(() => { if (from) setIntoId(undefined); }, [from]);

  const candidates = from ? models.filter(m => m.id !== from.id) : [];

  return (
    <Modal
      open={!!from}
      title={from ? `把「${from.name}」合并到…` : '合并模型'}
      width={520}
      onClose={onClose}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn
            variant="primary"
            disabled={!intoId}
            onClick={() => { const into = candidates.find(m => m.id === intoId); if (into) onConfirm(into); }}
          >
            合并
          </Btn>
        </>
      }
    >
      <Note>消除重复模型:「{from?.name}」的供给源会并入目标模型后删除源行;各供给源的上游真实名会保留。若两者在同一渠道上都有供给源,会提示先删除其一。</Note>
      {candidates.length === 0 ? (
        <Empty title="没有可合并的目标"><p>目录里只有这一个模型。</p></Empty>
      ) : (
        <Field label="选择目标模型">
          <Select value={intoId ?? ''} onChange={e => setIntoId(Number(e.target.value))}>
            <option value="">请选择目标模型</option>
            {candidates.map(m => (
              <option key={m.id} value={m.id}>{m.name}{m.displayName ? ` · 原始名 ${m.originalName}` : ''}（{m.offers.length} 家供给）</option>
            ))}
          </Select>
        </Field>
      )}
    </Modal>
  );
}

export default function Models() {
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
  const [mergeFrom, setMergeFrom] = useState<ModelCatalogItem | null>(null);
  const [busyId, setBusyId] = useState<number | null>(null);

  const { data: models = [], isLoading, isError, refetch } = useQuery({ queryKey: ['models'], queryFn: api.getModels });
  const { data: channels = [] } = useQuery({ queryKey: ['channels'], queryFn: api.getChannels });
  // 官方参考价:用于给「最低价」列加来源角标(只读,失败静默)。
  const { data: official = [] } = useQuery({
    queryKey: ['official-prices'],
    queryFn: () => api.officialPrices(),
    retry: 0,
    staleTime: 30_000,
  });
  const officialIndex = useMemo(() => buildOfficialIndex(official), [official]);
  /** 该模型任一供给源能对上官方参考价 → 返回第一条(用于角标 tooltip)。
   *  匹配优先级(显式绑定 → provider 直连 → 推断厂商)见 utils/official。 */
  const officialOf = (m: ModelCatalogItem): OfficialPriceView | undefined =>
    officialOfModel(officialIndex, m);

  const toggleModel = useMutation({
    mutationFn: (v: { id: number; enabled: boolean }) => {
      setBusyId(v.id);
      return api.toggleModel(v.id, v.enabled);
    },
    onSuccess: (r) => {
      // 启用时后端跳过零价供给源(issue #26),有条数就提示,否则不打扰。
      const skipped = r?.skippedZeroPrice ?? [];
      if (skipped.length > 0) {
        toast.info(`模型已启用,但有 ${skipped.length} 条供给源因无成本依据未启用:${skipped.join('、')}`);
      }
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onSettled: () => setBusyId(null),
    onError: () => toast.err('启停失败，请稍后重试'),
  });

  const createModel = useMutation({
    mutationFn: (d: ModelDraft) => api.createModel(d),
    onSuccess: () => {
      toast.ok('模型已创建');
      setAddOpen(false);
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onError: (e: Error) => toast.err(e.message || '创建失败'),
  });

  const syncModel = useMutation({
    mutationFn: (id: number) => api.syncModels(id),
    onSuccess: r => {
      toast.ok(`同步完成：新增 ${r.added} 个，更新 ${r.updated} 个，共 ${r.models.length} 个模型`);
      setSyncOpen(false);
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['channels'] });
    },
    onError: (e: Error) => toast.err(e.message || '同步失败'),
  });

  const deleteModel = useMutation({
    mutationFn: (id: number) => api.deleteModel(id),
    onSuccess: (_res, id) => {
      toast.ok('模型已删除');
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['channels'] });
      if (drawerId === id) setDrawerId(null);
    },
    onError: (e: Error) => toast.err(e.message || '删除失败'),
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
    confirm({
      title: `删除模型「${m.name}」`,
      body: '该模型下的全部供给源会一并删除，此操作不可恢复。',
      okText: '删除',
      danger: true,
      cancelText: '取消',
    }).then(ok => { if (ok) deleteModel.mutate(m.id); });
  };

  const mergeModel = useMutation({
    mutationFn: (v: { fromId: number; intoId: number }) => api.mergeModel(v.fromId, v.intoId),
    onSuccess: () => {
      toast.ok('模型已合并');
      setMergeFrom(null);
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['channels'] });
    },
    onError: (e: Error) => toast.err(e.message || '合并失败'),
  });

  const confirmMergeModel = (from: ModelCatalogItem, into: ModelCatalogItem) => {
    if (from.id === into.id) return;
    confirm({
      title: `把「${from.name}」合并到「${into.name}」`,
      body: `「${from.name}」的 ${from.offers.length} 条供给源将并入「${into.name}」，源模型随后删除。若两者在同一渠道上都有供给源会冲突，需先删除其中一个。`,
      okText: '合并',
      cancelText: '取消',
    }).then(ok => { if (ok) mergeModel.mutate({ fromId: from.id, intoId: into.id }); });
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
        toast.info('最多同时对比 4 个模型');
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

  const emptyNode = isError ? (
    <Empty title="模型目录加载失败">
      <p>无法读取模型目录，已有渠道与路由不受影响。</p>
      <Btn size="sm" onClick={() => void refetch()}>重试</Btn>
    </Empty>
  ) : models.length === 0 ? (
    <Empty title="模型目录还是空的">
      <p>可以「从渠道同步」把上游模型批量拉进来，也可以手动新增一个。</p>
      <Btn size="sm" variant="primary" disabled={channels.length === 0} onClick={() => setSyncOpen(true)}>从渠道同步</Btn>
    </Empty>
  ) : (
    <Empty title="没有符合条件的模型">
      <p>当前筛选（关键字 / 供应商 / 能力 / 上下文 / 启用状态）没有命中。</p>
      <Btn size="sm" onClick={clearFilters}>清除筛选</Btn>
    </Empty>
  );

  return (
    <div>
      <PageHead
        title="模型广场"
        sub="以模型为中心聚合多家供应商供给源，可对比价格与延迟、编排优先级"
        actions={
          <>
            <Btn onClick={() => setSyncOpen(true)}>从渠道同步</Btn>
            <Btn variant="primary" onClick={() => setAddOpen(true)}>新增模型</Btn>
          </>
        }
      />

      <div className="card">
        <div className="toolbar" style={{ padding: '12px 14px 0' }}>
          <Input value={kw} placeholder="搜索模型名" onChange={e => setKw(e.target.value)} style={{ width: 240 }} />
          <Select value={provider} onChange={e => setProvider(e.target.value)} style={{ width: 150 }}>
            <option value="">全部供应商</option>
            {providers.map(p => <option key={p} value={p}>{p}</option>)}
          </Select>
          <Select value={cap} onChange={e => setCap(e.target.value)} style={{ width: 140 }}>
            <option value="">全部能力</option>
            {capabilities.map(x => <option key={x} value={x}>{CAP_LABEL[x]}</option>)}
          </Select>
          <Select value={ctxRange} onChange={e => setCtxRange(e.target.value)} style={{ width: 150 }}>
            <option value="">上下文不限</option>
            <option value="s">&lt; 32K</option>
            <option value="m">32K – 128K</option>
            <option value="l">&gt; 128K</option>
          </Select>
          <Select value={sort} onChange={e => setSort(e.target.value as SortKey)} style={{ width: 140 }}>
            <option value="price">按最低价</option>
            <option value="latency">按最低延迟</option>
            <option value="hot">按调用量</option>
            <option value="ctx">按上下文</option>
          </Select>
          <Seg
            value={enableState}
            onChange={setEnableState}
            options={[
              { v: 'all', label: '全部' },
              { v: 'on', label: '仅已启用' },
              { v: 'off', label: '仅未启用' },
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
            <span className="cell-sub" style={{ fontSize: 13.5 }}>
              「仅已启用」按真实可用性过滤：目录启用且至少一个供给源启用
            </span>
            <div style={{ marginLeft: 'auto' }}>
              <Seg
                value={view}
                onChange={setView}
                options={[{ v: 'plaza', label: '广场' }, { v: 'table', label: '表格' }]}
              />
            </div>
          </div>

          {isLoading && models.length === 0 ? (
            <div className="mc-grid">
              {[0, 1, 2, 3].map(i => <div key={i} className="sk" style={{ height: 190, borderRadius: 5 }} />)}
            </div>
          ) : list.length === 0 ? (
            emptyNode
          ) : view === 'plaza' ? (
            <div className="mc-grid">
              {list.map(m => (
                <ModelCard
                  key={m.id}
                  model={m}
                  picked={compare.includes(m.id)}
                  busy={busyId === m.id}
                  officialPrice={officialOf(m)}
                  onOpen={() => setDrawerId(m.id)}
                  onToggleCompare={() => toggleCompare(m.id)}
                  onToggleEnabled={next => toggleModel.mutate({ id: m.id, enabled: next })}
                  onDelete={() => confirmDeleteModel(m)}
                  onMerge={() => setMergeFrom(m)}
                />
              ))}
            </div>
          ) : (
            <div className="table-wrap">
              <table className="tb">
                <thead>
                  <tr>
                    <th>模型</th>
                    <th className="right">供给源</th>
                    <th className="right">上下文</th>
                    <th className="right">最低输入价</th>
                    <th className="right">最低输出价</th>
                    <th>能力</th>
                    <th className="right">今日调用</th>
                    <th>启用</th>
                    <th className="right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {list.map(m => {
                    const p = bestPrice(m);
                    const op = officialOf(m);
                    const sym = (c?: string) => (c === 'CNY' ? '¥' : '$');
                    return (
                      <tr key={m.id} className="clickable" onClick={() => setDrawerId(m.id)}>
                        <td>
                          <span className="mono strong"><Copyable text={m.name}>{m.name}</Copyable></span>
                          {m.displayName && (
                            <div className="cell-sub mono">原始名 {m.originalName}</div>
                          )}
                        </td>
                        <td className="right num">
                          {m.offers.filter(o => o.enabled).length}<span className="cell-sub">/{m.offers.length}</span>
                        </td>
                        <td className="right num">{fmt.ctx(m.contextWindow)}</td>
                        <td className="right">
                          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, justifyContent: 'flex-end' }}>
                            <span className="num" style={{ color: p ? 'var(--brand)' : undefined }}>{p ? fmt.price(p.inP) : '—'}</span>
                            {op && (
                              <a
                                className="badge b-brand-o"
                                href={op.sourceUrl}
                                target="_blank"
                                rel="noreferrer"
                                style={{ fontSize: 11.5 }}
                                title={`官方参考价 ${sym(op.currency)}${op.inputPrice} / ${sym(op.currency)}${op.outputPrice}\n来源:${op.sourceUrl}\n抓取:${fmt.dt(op.fetchedAt)}`}
                              >
                                官方 ↗
                              </a>
                            )}
                          </span>
                        </td>
                        <td className="right num">{p ? fmt.price(p.outP) : '—'}</td>
                        <td>{m.capabilities.length ? m.capabilities.map(x => CAP_LABEL[x]).join(' · ') : '—'}</td>
                        <td className="right num">{fmt.k(m.todayRequests)}</td>
                        <td onClick={e => e.stopPropagation()}>
                          <Switch
                            checked={m.enabled}
                            loading={busyId === m.id}
                            onChange={next => toggleModel.mutate({ id: m.id, enabled: next })}
                          />
                        </td>
                        <td className="right" onClick={e => e.stopPropagation()}>
                          <span className="taglist" style={{ justifyContent: 'flex-end' }}>
                            <Btn size="sm" onClick={() => setDrawerId(m.id)}>编辑</Btn>
                            <Btn size="sm" variant="ghost" onClick={() => toggleModel.mutate({ id: m.id, enabled: !m.enabled })}>
                              {m.enabled ? '停用' : '启用'}
                            </Btn>
                          </span>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {/* 对比托盘：随滚动吸底，未选中任何模型时不出现 */}
      {compare.length > 0 && (
        <div
          style={{
            position: 'sticky', bottom: 8, marginTop: 18, zIndex: 30,
            background: 'var(--panel)', border: '1px solid var(--line)',
            borderRadius: 5, padding: '10px 14px',
            display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap',
          }}
        >
          <span className="cell-sub" style={{ fontSize: 13.5 }}>
            已选 <b className="num">{compare.length}</b> 个（最多 4 个）
          </span>
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
            {compareModels.map(m => (
              <span key={m.id} className="badge b-slate mono" style={{ gap: 7 }}>
                {m.name}
                <button
                  type="button"
                  className="btn btn-ghost btn-sm"
                  style={{ padding: '0 4px', lineHeight: 1 }}
                  aria-label={`从对比中移除 ${m.name}`}
                  onClick={() => toggleCompare(m.id)}
                >
                  ✕
                </button>
              </span>
            ))}
          </div>
          <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
            <Btn size="sm" onClick={() => setCompare([])}>清空</Btn>
            <Btn size="sm" variant="primary" onClick={() => setCompareOpen(true)}>开始对比</Btn>
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
      <MergeModal
        from={mergeFrom}
        models={models}
        onClose={() => setMergeFrom(null)}
        onConfirm={into => { if (mergeFrom) confirmMergeModel(mergeFrom, into); }}
      />
    </div>
  );
}
