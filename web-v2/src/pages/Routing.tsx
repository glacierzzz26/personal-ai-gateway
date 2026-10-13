import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import ProviderMark from '@/components/ProviderMark';
import { EmptyState, ErrorState, SkLines } from '@/components/States';
import { useSortableRows } from '@/hooks/useSortableRows';
import { Badge, Btn, Check, Field, Input, Note, PageHead, Seg, Select, Switch } from '@/ui/primitives';
import { Modal, confirm } from '@/ui/overlay';
import { toast } from '@/ui/toast';
import { Icon } from '@/ui/icons';
import { api } from '@/services/api';
import { channelLabel, channelMark } from '@/utils/channel';
import { fmt } from '@/utils/format';
import type { Channel, MatchMode, RouteRule, RouteStrategy, RuleDraft } from '@/types';

const MODE_TEXT: Record<string, string> = { prefix: '前缀', wildcard: '通配', regex: '正则' };
const STRATEGY_TEXT: Record<string, string> = { priority: '优先级', weight: '加权随机', latency: '低延迟' };

const MODE_OPTIONS: { v: MatchMode; label: string }[] = [
  { v: 'prefix', label: '前缀' },
  { v: 'wildcard', label: '通配' },
  { v: 'regex', label: '正则' },
];
const STRATEGY_OPTIONS: { v: RouteStrategy; label: string }[] = [
  { v: 'priority', label: '优先级' },
  { v: 'weight', label: '加权随机' },
  { v: 'latency', label: '低延迟' },
];
const PATTERN_PLACEHOLDER: Record<MatchMode, string> = {
  prefix: 'gpt-4*',
  wildcard: 'qwen-*/glm-*',
  regex: '^(deepseek-reasoner|o1-)',
};
const TIMEOUT_PRESETS = [10_000, 30_000, 60_000, 120_000, 300_000];

/** Select 中「无兜底」的哨兵串(表单 option 不便直接用 null) */
const NO_FALLBACK = '__none__';

/** 行快照 → 提交用完整草稿(后端 update 为整体替换,须带全字段) */
function ruleToDraft(r: RouteRule, enabled = r.enabled): RuleDraft {
  return {
    name: r.name,
    enabled,
    matchMode: r.matchMode,
    pattern: r.pattern,
    strategy: r.strategy,
    channelIds: r.channelIds,
    ...(r.strategy === 'weight' && r.weights && Object.keys(r.weights).length > 0
      ? { weights: { ...r.weights } }
      : {}),
    fallbackChannelId: r.fallbackChannelId ?? null,
    retry: r.retry,
    timeoutMs: r.timeoutMs,
  };
}

const timeoutLabel = (ms: number) => (ms >= 1000 ? `${Math.round(ms / 1000)}s` : `${ms}ms`);

/** 弹窗内表单态(手写 state 取代 antd Form);数值字段允许 '' 表示未填 */
interface RuleFormValues {
  name: string;
  enabled: boolean;
  matchMode: MatchMode;
  pattern: string;
  strategy: RouteStrategy;
  channelIds: number[];
  fallbackChannelId: number | typeof NO_FALLBACK;
  retry: number | '';
  timeoutMs: number;
}

const CREATE_INITIAL: RuleFormValues = {
  name: '', enabled: true, matchMode: 'prefix', pattern: '', strategy: 'priority',
  channelIds: [], fallbackChannelId: NO_FALLBACK, retry: 1, timeoutMs: 60_000,
};

/**
 * 新建/编辑规则弹窗(单个共享 Modal + 手写表单)。
 * weights 单独以 state 维护(JSON 对象键为渠道 id 数字),仅 strategy=weight 时展示。
 */
function RuleModal(props: {
  open: boolean;
  initial: RouteRule | null;
  channels: Channel[];
  onCancel: () => void;
  onSubmit: (draft: RuleDraft, id?: number) => Promise<void>;
}) {
  const { open, initial, channels, onCancel, onSubmit } = props;
  const [v, setV] = useState<RuleFormValues>(CREATE_INITIAL);
  const [weights, setWeights] = useState<Record<number, number>>({});
  const [saving, setSaving] = useState(false);
  const [errs, setErrs] = useState<Partial<Record<'name' | 'pattern' | 'channelIds' | 'retry', string>>>({});

  const channelName = (id: number) => channels.find(c => c.id === id)?.name ?? `#${id}`;

  // 打开时装载初值:编辑用行快照,新建用默认
  useEffect(() => {
    if (!open) return;
    setErrs({});
    if (initial) {
      setV({
        name: initial.name,
        enabled: initial.enabled,
        matchMode: initial.matchMode,
        pattern: initial.pattern,
        strategy: initial.strategy,
        channelIds: initial.channelIds,
        fallbackChannelId: initial.fallbackChannelId ?? NO_FALLBACK,
        retry: initial.retry,
        timeoutMs: initial.timeoutMs,
      });
      setWeights(initial.strategy === 'weight' && initial.weights ? { ...initial.weights } : {});
    } else {
      setV(CREATE_INITIAL);
      setWeights({});
    }
    // 弹窗每次打开/切换编辑对象时执行一次即可
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initial]);

  const set = <K extends keyof RuleFormValues>(k: K, val: RuleFormValues[K]) =>
    setV(p => ({ ...p, [k]: val }));

  const selectChannels = (ids: number[]) => {
    set('channelIds', ids);
    // 权重策略下给新选中的渠道补默认权重 100(相对值)
    setWeights(w => {
      const next = { ...w };
      for (const id of ids) if (next[id] == null) next[id] = 100;
      return next;
    });
  };

  // 校验规则逐条照搬原 antd Form.Item rules。
  const validate = (): boolean => {
    const e: typeof errs = {};
    if (!v.name.trim()) e.name = '请输入规则名称';
    if (!v.pattern.trim()) e.pattern = '请输入匹配表达式';
    if (!Array.isArray(v.channelIds) || v.channelIds.length === 0) e.channelIds = '请至少选择一个目标渠道';
    if (v.retry === '') e.retry = '必填';
    setErrs(e);
    return Object.keys(e).length === 0;
  };

  const submit = async () => {
    if (!validate()) return;
    const weightsObj: Record<number, number> = {};
    if (v.strategy === 'weight') {
      for (const id of v.channelIds) weightsObj[id] = weights[id] ?? 100;
    }
    const draft: RuleDraft = {
      name: v.name.trim(),
      enabled: v.enabled,
      matchMode: v.matchMode,
      pattern: v.pattern.trim(),
      strategy: v.strategy,
      channelIds: v.channelIds,
      ...(v.strategy === 'weight' ? { weights: weightsObj } : {}),
      fallbackChannelId:
        v.fallbackChannelId === NO_FALLBACK || v.fallbackChannelId == null ? null : v.fallbackChannelId,
      retry: typeof v.retry === 'number' ? v.retry : 0,
      timeoutMs: v.timeoutMs,
    };
    setSaving(true);
    try {
      await onSubmit(draft, initial?.id);
    } catch {
      // 错误信息已由父级提示,保持弹窗打开以便修改
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={initial ? '编辑路由规则' : '新建路由规则'}
      open={open}
      onClose={onCancel}
      width={600}
      footer={
        <>
          <Btn onClick={onCancel}>取消</Btn>
          <Btn variant="primary" loading={saving} onClick={submit}>{initial ? '保存' : '创建'}</Btn>
        </>
      }
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 4 }}>
        <Switch checked={v.enabled} onChange={nv => set('enabled', nv)} />
        <span style={{ fontSize: 13, color: 'var(--t2)' }}>启用该规则</span>
      </div>

      <Field label="规则名称" err={errs.name}>
        <Input placeholder="例如：GPT 高优转发" maxLength={50} value={v.name} onChange={e => set('name', e.target.value)} err={!!errs.name} />
      </Field>

      <div style={{ display: 'flex', gap: 12 }}>
        <Field label="匹配方式" style={{ flex: '0 0 220px' }}>
          <Seg value={v.matchMode} onChange={nv => set('matchMode', nv)} options={MODE_OPTIONS} />
        </Field>
        <Field label="匹配表达式" err={errs.pattern} style={{ flex: 1 }}>
          <Input
            placeholder={PATTERN_PLACEHOLDER[v.matchMode]}
            className="mono"
            value={v.pattern}
            onChange={e => set('pattern', e.target.value)}
            err={!!errs.pattern}
          />
        </Field>
      </div>

      <Field label="分发策略" hint="主渠道不可用时按此策略在目标渠道间选择">
        <Seg value={v.strategy} onChange={nv => set('strategy', nv)} options={STRATEGY_OPTIONS} />
      </Field>

      <Field label="目标渠道" hint="从「渠道管理」中选择参与分发的渠道" err={errs.channelIds}>
        {channels.length === 0 ? (
          <div style={{ fontSize: 13, color: 'var(--t3)' }}>暂无可用渠道，请先在「渠道管理」创建。</div>
        ) : (
          <div
            style={{
              border: `1px solid ${errs.channelIds ? 'var(--err)' : 'var(--line)'}`,
              borderRadius: 4, maxHeight: 180, overflowY: 'auto',
              padding: '9px 11px', display: 'flex', flexDirection: 'column', gap: 8,
            }}
          >
            {channels.map(c => (
              <Check
                key={c.id}
                checked={v.channelIds.includes(c.id)}
                onChange={() =>
                  selectChannels(
                    v.channelIds.includes(c.id)
                      ? v.channelIds.filter(x => x !== c.id)
                      : [...v.channelIds, c.id],
                  )
                }
              >
                <span className="mono">{c.name}</span> · {channelLabel(c)}
              </Check>
            ))}
          </div>
        )}
      </Field>

      {v.strategy === 'weight' && (
        <Field label="渠道权重（相对值，无需合计 100）">
          {v.channelIds.length === 0 ? (
            <div style={{ fontSize: 13, color: 'var(--t3)' }}>
              请先在上方选择目标渠道。
            </div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {v.channelIds.map(id => (
                <div key={id} className="rule-row">
                  <span className="ellip" style={{ fontSize: 13 }}>{channelName(id)}</span>
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                    <Input
                      type="number"
                      min={1}
                      step={1}
                      value={weights[id] ?? 100}
                      onChange={e => {
                        const n = Number(e.target.value);
                        setWeights(w => ({ ...w, [id]: e.target.value === '' || Number.isNaN(n) ? 100 : Math.max(1, Math.floor(n)) }));
                      }}
                      style={{ width: 110 }}
                    />
                    <span style={{ fontSize: 12.5, color: 'var(--t3)' }}>权重</span>
                  </span>
                </div>
              ))}
            </div>
          )}
        </Field>
      )}

      <Field label="兜底渠道（全部目标渠道失败后使用）">
        <Select
          value={v.fallbackChannelId}
          onChange={e => set('fallbackChannelId', e.target.value === NO_FALLBACK ? NO_FALLBACK : Number(e.target.value))}
        >
          <option value={NO_FALLBACK}>无兜底</option>
          {channels.map(c => (
            <option key={c.id} value={c.id}>{c.name} · {channelLabel(c)}</option>
          ))}
        </Select>
      </Field>

      <div style={{ display: 'flex', gap: 12 }}>
        <Field label="重试次数" err={errs.retry} style={{ flex: 1 }}>
          <Input
            type="number"
            min={0}
            max={10}
            step={1}
            value={v.retry}
            onChange={e => set('retry', e.target.value === '' ? '' : Math.max(0, Math.min(10, Math.floor(Number(e.target.value) || 0))))}
            err={!!errs.retry}
          />
        </Field>
        <Field label="超时时间" hint="该规则命中的请求在单个候选上的等待上限" style={{ flex: 1 }}>
          <Select value={v.timeoutMs} onChange={e => set('timeoutMs', Number(e.target.value))}>
            {TIMEOUT_PRESETS.map(ms => (
              <option key={ms} value={ms}>{timeoutLabel(ms)}</option>
            ))}
          </Select>
        </Field>
      </div>
    </Modal>
  );
}

export default function Routing() {
  const qc = useQueryClient();
  const [editor, setEditor] = useState<{ open: boolean; initial: RouteRule | null }>({ open: false, initial: null });
  const [toggling, setToggling] = useState<ReadonlySet<number>>(new Set());

  const { data: rules = [], isLoading, isError, refetch } = useQuery({
    queryKey: ['rules'],
    queryFn: api.getRules,
    retry: 0,
  });
  const { data: channels = [] } = useQuery({
    queryKey: ['channels'],
    queryFn: api.getChannels,
    retry: 0,
  });

  const refreshRules = () => qc.invalidateQueries({ queryKey: ['rules'] });
  const chNameMap = new Map(channels.map(c => [c.id, c.name]));
  const channelName = (id: number) => chNameMap.get(id) ?? `#${id}`;

  /* —— 拖拽重排 —— */
  const reorder = useMutation({
    mutationFn: (v: { from: number; insertAt: number }) => api.reorderRules(v.from, v.insertAt),
    onMutate: async v => {
      await qc.cancelQueries({ queryKey: ['rules'] });
      const prev = qc.getQueryData<RouteRule[]>(['rules']);
      // 与服务端 MoveRule 同语义:先移除 from,再插入到 insertAt 位
      qc.setQueryData<RouteRule[]>(['rules'], old => {
        if (!old) return old;
        const next = [...old];
        const [moved] = next.splice(v.from, 1);
        next.splice(v.insertAt, 0, moved);
        return next;
      });
      return { prev };
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData(['rules'], ctx.prev);
      toast.err('顺序更新失败，已恢复原顺序');
    },
    onSuccess: (list, v) => {
      qc.setQueryData(['rules'], list);
      const target = v.insertAt > v.from ? v.insertAt - 1 : v.insertAt;
      toast.ok(`顺序已更新 → 第 ${target + 1} 位`);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ['rules'] }),
  });

  const { onRow, handleProps } = useSortableRows((from, insertAt) => {
    if (reorder.isPending) return;
    reorder.mutate({ from, insertAt });
  });

  /* —— 行内启用/停用(整体替换,须带全量快照;连点时读缓存最新行防闪回) —— */
  const toggle = useMutation({
    mutationFn: async (v: { row: RouteRule; enabled: boolean }) => {
      const latest = (qc.getQueryData<RouteRule[]>(['rules']) ?? []).find(x => x.id === v.row.id) ?? v.row;
      return api.updateRule(v.row.id, ruleToDraft(latest, v.enabled));
    },
    onMutate: async v => {
      setToggling(prev => new Set(prev).add(v.row.id));
      await qc.cancelQueries({ queryKey: ['rules'] });
      const prev = qc.getQueryData<RouteRule[]>(['rules']);
      qc.setQueryData<RouteRule[]>(['rules'], old =>
        old?.map(x => (x.id === v.row.id ? { ...x, enabled: v.enabled } : x)),
      );
      return { prev };
    },
    onError: (_e, v, ctx) => {
      if (ctx?.prev) qc.setQueryData(['rules'], ctx.prev);
      toast.err(`「${v.row.name}」启用状态更新失败`);
    },
    onSuccess: updated => {
      qc.setQueryData<RouteRule[]>(['rules'], old =>
        (old ?? []).map(x => (x.id === updated.id ? updated : x)),
      );
      toast.ok(updated.enabled ? '规则已启用' : '规则已停用');
    },
    onSettled: (_data, _err, v) => {
      setToggling(prev => {
        const n = new Set(prev);
        n.delete(v.row.id);
        return n;
      });
      qc.invalidateQueries({ queryKey: ['rules'] });
    },
  });

  /* —— 删除 —— */
  const remove = useMutation({
    mutationFn: (id: number) => api.deleteRule(id),
    onSuccess: () => {
      toast.ok('规则已删除');
      refreshRules();
    },
    onError: e => toast.err(e instanceof Error ? e.message : '删除失败'),
  });

  const confirmDelete = (r: RouteRule) => {
    confirm({
      title: `删除规则「${r.name}」？`,
      body: '删除后该匹配将不再生效，其余规则顺序会自动重排。',
      okText: '删除',
      danger: true,
      cancelText: '取消',
    }).then(ok => { if (ok) remove.mutateAsync(r.id).catch(() => undefined); });
  };

  /* —— 新建 / 编辑保存(弹窗内统一走这里) —— */
  const saveRule = async (draft: RuleDraft, id?: number) => {
    try {
      if (id != null) {
        await api.updateRule(id, draft);
        toast.ok('规则已保存');
      } else {
        await api.createRule(draft);
        toast.ok('规则已创建');
      }
      setEditor({ open: false, initial: null });
      refreshRules();
    } catch (e) {
      toast.err(e instanceof Error ? e.message : '保存失败');
      throw e;
    }
  };

  return (
    <div>
      <PageHead
        title="路由规则"
        sub="请求进来时按模型名匹配规则；规则优先级高于模型广场内部的供给源顺序"
        actions={
          <Btn variant="primary" onClick={() => setEditor({ open: true, initial: null })}>
            新建规则
          </Btn>
        }
      />

      {/* 排序语义说明：拖拽是这一页的核心交互，必须显式写出来 */}
      <div style={{ marginBottom: 14 }}>
        <Note>
          <b>自上而下匹配</b>
          <span>　拖动左侧手柄调整顺序（或 Ctrl/Alt + ↑↓），越靠前优先级越高。未命中任何规则的请求将走默认渠道（供给源顺序）。</span>
        </Note>
      </div>

      {isError ? (
        <div className="card">
          <div className="card-body">
            <ErrorState
              title="路由规则加载失败"
              desc="无法读取规则列表。未命中规则的请求仍按默认渠道转发。"
              onRetry={() => void refetch()}
            />
          </div>
        </div>
      ) : (
        <div className="card">
          {isLoading && rules.length === 0 ? (
            <div className="card-body">
              <SkLines rows={['w80', 'w60', 'w80', 'w60']} />
            </div>
          ) : rules.length === 0 ? (
            <div className="card-body">
              <EmptyState
                title="还没有路由规则"
                desc="没有规则时，所有请求都走默认渠道（模型广场里的供给源顺序）。需要按模型名分流时再建规则。"
                action={<Btn size="sm" variant="primary" onClick={() => setEditor({ open: true, initial: null })}>新建规则</Btn>}
              />
            </div>
          ) : (
            <div className="table-wrap">
              <table className="tb" style={{ tableLayout: 'fixed' }}>
                <thead>
                  <tr>
                    <th style={{ width: 44 }}></th>
                    {/* 顺序号并入名称列(拖动柄已表达先后,序号只是辅助);弹性列吸收剩余宽度 */}
                    <th>名称</th>
                    <th className="right" style={{ width: 64 }}>启用</th>
                    <th style={{ width: 190 }}>匹配条件</th>
                    <th style={{ width: 190 }}>分发策略</th>
                    <th style={{ width: 190 }}>目标渠道</th>
                    <th style={{ width: 144 }}>兜底 / 重试</th>
                    <th className="right" style={{ width: 120 }}>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {rules.map((r, i) => (
                    <tr key={r.id} {...onRow(r, i)}>
                      <td>
                        <span {...handleProps(i)} aria-label="拖动调整顺序" style={{ cursor: 'grab', color: 'var(--t3)', display: 'inline-flex' }}>
                          <Icon n="grip" />
                        </span>
                      </td>
                      <td>
                        <div style={{ display: 'flex', gap: 8, alignItems: 'baseline' }}>
                          <span className="num" style={{ color: 'var(--t3)', fontSize: 12.5 }}>{i + 1}</span>
                          <div style={{ minWidth: 0 }}>
                            <div style={{ fontWeight: 500, color: r.enabled ? 'var(--t1)' : 'var(--t3)' }}>{r.name}</div>
                            <div style={{ fontSize: 12.5, color: 'var(--t3)' }}>
                              命中 <span className="num">{fmt.n(r.hit)}</span> 次
                            </div>
                          </div>
                        </div>
                      </td>
                      <td className="right">
                        <span aria-label={`${r.enabled ? '停用' : '启用'}规则 ${r.name}`}>
                          <Switch
                            checked={r.enabled}
                            disabled={toggling.has(r.id)}
                            onChange={c => toggle.mutate({ row: r, enabled: c })}
                          />
                        </span>
                      </td>
                      <td>
                        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
                          <Badge tone="slate">{MODE_TEXT[r.matchMode]}</Badge>
                          <span className="mono ellip" title={r.pattern} style={{ maxWidth: 110 }}>{r.pattern}</span>
                        </span>
                      </td>
                      <td>
                        <Badge tone={r.strategy === 'weight' ? 'brand-o' : 'slate'}>{STRATEGY_TEXT[r.strategy]}</Badge>
                        {r.strategy === 'weight' && r.weights && Object.keys(r.weights).length > 0 && (
                          <div style={{ fontSize: 12.5, color: 'var(--t3)', marginTop: 5 }}>
                            {Object.entries(r.weights)
                              .map(([k, w]) => `${channelName(Number(k))} ${w}`)
                              .join(' / ')}
                          </div>
                        )}
                      </td>
                      <td>
                        {r.channelIds.length === 0 ? (
                          <span style={{ color: 'var(--t3)' }}>—</span>
                        ) : (
                          <span style={{ display: 'inline-flex', gap: 5, flexWrap: 'wrap' }}>
                            {r.channelIds.map(id => {
                              const ch = channels.find(c => c.id === id);
                              return (
                                <span className="badge b-slate" key={id} style={{ gap: 5 }}>
                                  {ch && <ProviderMark name={channelMark(ch)} size={14} />}
                                  {ch?.name ?? `#${id}`}
                                </span>
                              );
                            })}
                          </span>
                        )}
                      </td>
                      {/* 兜底渠道 / 重试次数 / 超时 三合一 —— 都是「主渠道失败后怎么办」 */}
                      <td style={{ fontSize: 13 }}>
                        {r.fallbackChannelId == null ? (
                          <span style={{ color: 'var(--t3)' }}>无兜底</span>
                        ) : (
                          <Badge tone="amber">{channelName(r.fallbackChannelId)}</Badge>
                        )}
                        <div style={{ fontSize: 12.5, color: 'var(--t3)', marginTop: 4 }}>
                          重试 <span className="num">{r.retry}</span> 次 · {timeoutLabel(r.timeoutMs)}
                        </div>
                      </td>
                      <td className="right">
                        <span className="taglist" style={{ justifyContent: 'flex-end' }}>
                          <Btn size="sm" onClick={() => setEditor({ open: true, initial: r })}>编辑</Btn>
                          <Btn size="sm" variant="danger" onClick={() => confirmDelete(r)}>删除</Btn>
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      <RuleModal
        open={editor.open}
        initial={editor.initial}
        channels={channels}
        onCancel={() => setEditor({ open: false, initial: null })}
        onSubmit={saveRule}
      />
    </div>
  );
}
