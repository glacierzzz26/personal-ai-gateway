import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import ProviderMark from '@/components/ProviderMark';
import { EmptyState, ErrorState } from '@/components/States';
import { useSortableRows } from '@/hooks/useSortableRows';
import { Badge, Button, Checkbox, Field, Input, Select, Strip, Switch } from '@/ui/primitives';
import { Modal, confirm } from '@/ui/overlay';
import { message } from '@/ui/toast';
import { api } from '@/services/api';
import { channelLabel, channelMark } from '@/utils/channel';
import { fmt } from '@/utils/format';
import type { Channel, MatchMode, RouteRule, RouteStrategy, RuleDraft } from '@/types';

const MODE_TEXT: Record<string, string> = { prefix: '前缀', wildcard: '通配', regex: '正则' };
const STRATEGY_TEXT: Record<string, string> = { priority: '优先级', weight: '加权随机', latency: '低延迟' };

const MODE_OPTIONS: { value: MatchMode; label: string }[] = [
  { value: 'prefix', label: '前缀' },
  { value: 'wildcard', label: '通配' },
  { value: 'regex', label: '正则' },
];
const STRATEGY_OPTIONS: { value: RouteStrategy; label: string }[] = [
  { value: 'priority', label: '优先级' },
  { value: 'weight', label: '加权随机' },
  { value: 'latency', label: '低延迟' },
];
const PATTERN_PLACEHOLDER: Record<MatchMode, string> = {
  prefix: 'gpt-4*',
  wildcard: 'qwen-*/glm-*',
  regex: '^(deepseek-reasoner|o1-)',
};
const TIMEOUT_PRESETS = [10_000, 30_000, 60_000, 120_000, 300_000];

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

const NO_FALLBACK = 0;

interface RuleFormValues {
  name: string;
  enabled: boolean;
  matchMode: MatchMode;
  pattern: string;
  strategy: RouteStrategy;
  channelIds: number[];
  fallbackChannelId: number;
  retry: number;
  timeoutMs: number;
}

const CREATE_INITIAL: RuleFormValues = {
  name: '', enabled: true, matchMode: 'prefix', pattern: '', strategy: 'priority',
  channelIds: [], fallbackChannelId: NO_FALLBACK, retry: 1, timeoutMs: 60_000,
};

/**
 * 新建/编辑规则弹窗(受控 state + 手写校验)。
 * weights 单独以 state 维护(键为渠道 id),仅 strategy=weight 时展示。
 * 目标渠道用勾选列表(自绘 Checkbox),后端仍收 channelIds: number[]。
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
  const [errors, setErrors] = useState<Partial<Record<keyof RuleFormValues, string>>>({});
  const [weights, setWeights] = useState<Record<number, number>>({});
  const [saving, setSaving] = useState(false);

  const set = <K extends keyof RuleFormValues>(key: K, val: RuleFormValues[K]) => {
    setV(s => ({ ...s, [key]: val }));
    setErrors(e => (e[key] ? { ...e, [key]: undefined } : e));
  };

  // 打开时装载初值:编辑用行快照,新建用默认
  useEffect(() => {
    if (!open) return;
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
    setErrors({});
  }, [open, initial]);

  const toggleChannel = (id: number, checked: boolean) => {
    const ids = checked ? [...v.channelIds, id] : v.channelIds.filter(x => x !== id);
    set('channelIds', ids);
    // 权重策略下给新选中的渠道补默认权重 100(相对值)
    if (checked) setWeights(w => (w[id] == null ? { ...w, [id]: 100 } : w));
  };

  const channelName = (id: number) => channels.find(c => c.id === id)?.name ?? `#${id}`;

  const submit = async () => {
    const errs: Partial<Record<keyof RuleFormValues, string>> = {};
    if (!v.name.trim()) errs.name = '请输入规则名称';
    if (!v.pattern.trim()) errs.pattern = '请输入匹配表达式';
    if (v.channelIds.length === 0) errs.channelIds = '请至少选择一个目标渠道';
    setErrors(errs);
    if (Object.keys(errs).length) return;

    const weightsObj: Record<number, number> = {};
    if (v.strategy === 'weight') for (const id of v.channelIds) weightsObj[id] = weights[id] ?? 100;
    const draft: RuleDraft = {
      name: v.name.trim(),
      enabled: v.enabled,
      matchMode: v.matchMode,
      pattern: v.pattern.trim(),
      strategy: v.strategy,
      channelIds: v.channelIds,
      ...(v.strategy === 'weight' ? { weights: weightsObj } : {}),
      fallbackChannelId: v.fallbackChannelId === NO_FALLBACK ? null : v.fallbackChannelId,
      retry: v.retry,
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

  if (!open) return null;

  return (
    <div className="overlay" style={{ zIndex: 70 }}>
      <Modal
        title={initial ? '编辑路由规则' : '新建路由规则'}
        width={600}
        onClose={onCancel}
        footer={
          <>
            <Button onClick={onCancel}>取消</Button>
            <Button variant="primary" loading={saving} onClick={submit}>
              {initial ? '保存' : '创建'}
            </Button>
          </>
        }
      >
        <div className="stack">
        <div className="hstack">
          <Switch checked={v.enabled} onChange={c => set('enabled', c)} />
          <span className="muted">启用该规则</span>
        </div>

        <Field label="规则名称" error={errors.name}>
          <Input value={v.name} maxLength={50} placeholder="例如：GPT 高优转发" onChange={e => set('name', e.target.value)} />
        </Field>

        <div className="field-row">
          <Field label="匹配方式">
            <Select
              value={v.matchMode}
              options={MODE_OPTIONS}
              onChange={e => set('matchMode', e.target.value as MatchMode)}
            />
          </Field>
          <Field label="匹配表达式" error={errors.pattern}>
            <Input
              className="mono"
              value={v.pattern}
              placeholder={PATTERN_PLACEHOLDER[v.matchMode]}
              onChange={e => set('pattern', e.target.value)}
            />
          </Field>
        </div>

        <Field label="分发策略">
          <Select
            value={v.strategy}
            options={STRATEGY_OPTIONS}
            onChange={e => set('strategy', e.target.value as RouteStrategy)}
          />
        </Field>

        <Field label="目标渠道" error={errors.channelIds}>
          {channels.length === 0 ? (
            <span className="muted">尚无渠道，请先到「渠道管理」创建。</span>
          ) : (
            <div className="stack-sm" style={{ maxHeight: 168, overflowY: 'auto' }}>
              {channels.map(c => (
                <Checkbox
                  key={c.id}
                  checked={v.channelIds.includes(c.id)}
                  onChange={ck => toggleChannel(c.id, ck)}
                >
                  {c.name} · {channelLabel(c)}
                </Checkbox>
              ))}
            </div>
          )}
        </Field>

        {v.strategy === 'weight' && (
          <Field label="渠道权重（相对值，无需合计 100）">
            {v.channelIds.length === 0 ? (
              <span className="muted">请先在上方勾选目标渠道。</span>
            ) : (
              <div className="stack-sm">
                {v.channelIds.map(id => (
                  <div key={id} className="hstack" style={{ gap: 10 }}>
                    <span className="grow ellip">{channelName(id)}</span>
                    <Input
                      type="number"
                      min={1}
                      style={{ width: 96 }}
                      value={weights[id] ?? 100}
                      onChange={e => setWeights(w => ({ ...w, [id]: Number(e.target.value) || 100 }))}
                    />
                  </div>
                ))}
              </div>
            )}
          </Field>
        )}

        <Field label="兜底渠道（全部目标渠道失败后使用）">
          <Select
            value={v.fallbackChannelId}
            onChange={e => set('fallbackChannelId', Number(e.target.value))}
            options={[
              { value: NO_FALLBACK, label: '无兜底' },
              ...channels.map(c => ({ value: c.id, label: `${c.name} · ${channelLabel(c)}` })),
            ]}
          />
        </Field>

        <div className="field-row">
          <Field label="重试次数">
            <Input
              type="number"
              min={0}
              max={10}
              value={v.retry}
              onChange={e => set('retry', Number(e.target.value) || 0)}
            />
          </Field>
          <Field label="超时时间" hint="该规则命中的请求在单个候选上的等待上限">
            <Select
              value={v.timeoutMs}
              onChange={e => set('timeoutMs', Number(e.target.value))}
              options={TIMEOUT_PRESETS.map(ms => ({ value: ms, label: timeoutLabel(ms) }))}
            />
          </Field>
        </div>
        </div>
      </Modal>
    </div>
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
      message.error('顺序更新失败，已恢复原顺序');
    },
    onSuccess: (list, v) => {
      qc.setQueryData(['rules'], list);
      const target = v.insertAt > v.from ? v.insertAt - 1 : v.insertAt;
      message.success(`顺序已更新 → 第 ${target + 1} 位`);
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
      message.error(`「${v.row.name}」启用状态更新失败`);
    },
    onSuccess: updated => {
      qc.setQueryData<RouteRule[]>(['rules'], old =>
        (old ?? []).map(x => (x.id === updated.id ? updated : x)),
      );
      message.success(updated.enabled ? '规则已启用' : '规则已停用');
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
      message.success('规则已删除');
      refreshRules();
    },
    onError: e => message.error(e instanceof Error ? e.message : '删除失败'),
  });

  const confirmDelete = (r: RouteRule) => {
    void confirm({
      title: `删除规则「${r.name}」？`,
      content: '删除后该匹配将不再生效，其余规则顺序会自动重排。',
      okText: '删除',
      danger: true,
    }).then(ok => {
      if (ok) remove.mutate(r.id);
    });
  };

  /* —— 新建 / 编辑保存(弹窗内统一走这里) —— */
  const saveRule = async (draft: RuleDraft, id?: number) => {
    try {
      if (id != null) {
        await api.updateRule(id, draft);
        message.success('规则已保存');
      } else {
        await api.createRule(draft);
        message.success('规则已创建');
      }
      setEditor({ open: false, initial: null });
      refreshRules();
    } catch (e) {
      message.error(e instanceof Error ? e.message : '保存失败');
      throw e;
    }
  };

  const openEditor = (initial: RouteRule | null) => setEditor({ open: true, initial });

  return (
    <>
      <PageHeader
        title="路由规则"
        desc="请求进来时按模型名匹配规则；规则优先级高于模型广场内部的供给源顺序"
        extra={
          <Button variant="primary" onClick={() => openEditor(null)}>
            新建规则
          </Button>
        }
      />

      <Blocks>
        {/* 排序语义说明：拖拽是这一页的核心交互，必须显式写出来 */}
        <Strip tone="aux">
          <span className="st-title">自上而下匹配</span>
          <span className="st-desc">拖动左侧手柄调整顺序，越靠前优先级越高。未命中任何规则的请求将走默认渠道（供给源顺序）。</span>
        </Strip>

        <BlockCard>
          {isError ? (
            <div className="card-body">
              <ErrorState
                title="路由规则加载失败"
                desc="无法读取规则列表。未命中规则的请求仍按默认渠道转发。"
                onRetry={() => void refetch()}
              />
            </div>
          ) : !isLoading && rules.length === 0 ? (
            <div className="card-body">
              <EmptyState
                title="还没有路由规则"
                desc="没有规则时，所有请求都走默认渠道（模型广场里的供给源顺序）。需要按模型名分流时再建规则。"
                action={<Button variant="primary" size="sm" onClick={() => openEditor(null)}>新建规则</Button>}
              />
            </div>
          ) : (
            <div className="table-wrap">
              <table className="tb">
                <thead>
                  <tr>
                    <th style={{ width: 36 }} />
                    <th>名称</th>
                    <th style={{ width: 64 }}>启用</th>
                    <th style={{ width: 168 }}>匹配条件</th>
                    <th style={{ width: 168 }}>分发策略</th>
                    <th style={{ width: 168 }}>目标渠道</th>
                    <th style={{ width: 144 }}>兜底 / 重试</th>
                    <th className="right" style={{ width: 120 }}>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {rules.map((r, i) => (
                    <tr key={r.id} {...onRow(r, i)}>
                      <td>
                        <span {...handleProps(i)} aria-label="拖动调整顺序">⋮⋮</span>
                      </td>
                      <td>
                        <div className="hstack" style={{ gap: 8, alignItems: 'baseline' }}>
                          <span className="num cell-sub">{i + 1}</span>
                          <div style={{ minWidth: 0 }}>
                            <div className={r.enabled ? 'strong' : 'muted'}>{r.name}</div>
                            <div className="cell-sub">
                              命中 <span className="num">{fmt.n(r.hit)}</span> 次
                            </div>
                          </div>
                        </div>
                      </td>
                      <td>
                        <Switch
                          checked={r.enabled}
                          disabled={toggling.has(r.id)}
                          onChange={c => toggle.mutate({ row: r, enabled: c })}
                        />
                      </td>
                      <td>
                        <span className="hstack" style={{ gap: 7 }}>
                          <Badge tone="slate">{MODE_TEXT[r.matchMode]}</Badge>
                          <span className="mono">{r.pattern}</span>
                        </span>
                      </td>
                      <td>
                        <div>
                          <Badge tone={r.strategy === 'weight' ? 'brand' : 'slate'}>{STRATEGY_TEXT[r.strategy]}</Badge>
                          {r.strategy === 'weight' && r.weights && Object.keys(r.weights).length > 0 && (
                            <div className="cell-sub" style={{ marginTop: 5 }}>
                              {Object.entries(r.weights)
                                .map(([k, w]) => `${channelName(Number(k))} ${w}`)
                                .join(' / ')}
                            </div>
                          )}
                        </div>
                      </td>
                      <td>
                        {r.channelIds.length === 0 ? (
                          <span className="muted">—</span>
                        ) : (
                          <span className="hstack wrap" style={{ gap: 5 }}>
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
                      <td>
                        <div>
                          {r.fallbackChannelId == null ? (
                            <span className="muted">无兜底</span>
                          ) : (
                            <Badge tone="amber">{channelName(r.fallbackChannelId)}</Badge>
                          )}
                          <div className="cell-sub" style={{ marginTop: 4 }}>
                            重试 <span className="num">{r.retry}</span> 次 · {timeoutLabel(r.timeoutMs)}
                          </div>
                        </div>
                      </td>
                      <td className="right">
                        <span className="hstack" style={{ gap: 4, justifyContent: 'flex-end' }}>
                          <Button size="sm" onClick={() => openEditor(r)}>编辑</Button>
                          <Button size="sm" variant="danger" onClick={() => confirmDelete(r)}>删除</Button>
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </BlockCard>
      </Blocks>

      <RuleModal
        open={editor.open}
        initial={editor.initial}
        channels={channels}
        onCancel={() => setEditor({ open: false, initial: null })}
        onSubmit={saveRule}
      />
    </>
  );
}
