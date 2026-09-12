import { useEffect, useState } from 'react';
import {
  App, Button, Empty, Form, Input, InputNumber, Modal, Select, Space, Switch, Table,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import ProviderMark from '@/components/ProviderMark';
import { EmptyState, ErrorState } from '@/components/States';
import { useSortableRows } from '@/hooks/useSortableRows';
import { api } from '@/services/api';
import { fmt } from '@/utils/format';
import { TOKENS } from '@/styles/tokens';
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

interface RuleFormValues {
  name: string;
  enabled: boolean;
  matchMode: MatchMode;
  pattern: string;
  strategy: RouteStrategy;
  channelIds: number[];
  fallbackChannelId?: number | typeof NO_FALLBACK;
  retry: number;
  timeoutMs: number;
}

const CREATE_INITIAL: RuleFormValues = {
  name: '', enabled: true, matchMode: 'prefix', pattern: '', strategy: 'priority',
  channelIds: [], fallbackChannelId: NO_FALLBACK, retry: 1, timeoutMs: 60_000,
};

/**
 * 新建/编辑规则弹窗(单个共享 Modal + Form)。
 * weights 单独以 state 维护(JSON 对象键为渠道 id 字符串),仅 strategy=weight 时展示。
 */
function RuleModal(props: {
  open: boolean;
  initial: RouteRule | null;
  channels: Channel[];
  onCancel: () => void;
  onSubmit: (draft: RuleDraft, id?: number) => Promise<void>;
}) {
  const { open, initial, channels, onCancel, onSubmit } = props;
  const [form] = Form.useForm<RuleFormValues>();
  const [weights, setWeights] = useState<Record<number, number>>({});
  const [saving, setSaving] = useState(false);

  const strategy = (Form.useWatch('strategy', form) as RouteStrategy | undefined) ?? 'priority';
  const matchMode = (Form.useWatch('matchMode', form) as MatchMode | undefined) ?? 'prefix';
  const channelIds = (Form.useWatch('channelIds', form) as number[] | undefined) ?? [];

  const channelName = (id: number) => channels.find(c => c.id === id)?.name ?? `#${id}`;

  // 打开时装载初值:编辑用行快照,新建用默认
  useEffect(() => {
    if (!open) return;
    if (initial) {
      form.setFieldsValue({
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
      form.resetFields();
      form.setFieldsValue(CREATE_INITIAL);
      setWeights({});
    }
    // 弹窗每次打开/切换编辑对象时执行一次即可
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initial]);

  const selectChannels = (ids: number[]) => {
    form.setFieldValue('channelIds', ids);
    // 权重策略下给新选中的渠道补默认权重 100(相对值)
    setWeights(w => {
      const next = { ...w };
      for (const id of ids) if (next[id] == null) next[id] = 100;
      return next;
    });
  };

  const submit = async () => {
    const v = await form.validateFields();
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

  const opts = channels.map(c => ({ value: c.id, label: `${c.name} · ${c.provider}` }));

  return (
    <Modal
      title={initial ? '编辑路由规则' : '新建路由规则'}
      open={open}
      onCancel={onCancel}
      onOk={submit}
      confirmLoading={saving}
      okText={initial ? '保存' : '创建'}
      cancelText="取消"
      destroyOnHidden
      width={600}
    >
      <Form form={form} layout="vertical" requiredMark={false} preserve={false}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 4 }}>
          <Form.Item name="enabled" valuePropName="checked" noStyle>
            <Switch />
          </Form.Item>
          <span style={{ fontSize: 13, color: 'var(--gw-text-2)' }}>启用该规则</span>
        </div>

        <Form.Item
          name="name"
          label="规则名称"
          rules={[{ required: true, whitespace: true, message: '请输入规则名称' }]}
        >
          <Input placeholder="例如：GPT 高优转发" maxLength={50} />
        </Form.Item>

        <div style={{ display: 'flex', gap: 12 }}>
          <Form.Item
            name="matchMode"
            label="匹配方式"
            rules={[{ required: true }]}
            style={{ flex: '0 0 140px' }}
          >
            <Select options={MODE_OPTIONS} />
          </Form.Item>
          <Form.Item
            name="pattern"
            label="匹配表达式"
            rules={[{ required: true, whitespace: true, message: '请输入匹配表达式' }]}
            style={{ flex: 1 }}
          >
            <Input placeholder={PATTERN_PLACEHOLDER[matchMode]} className="gw-mono" />
          </Form.Item>
        </div>

        <Form.Item
          name="strategy"
          label="分发策略"
          rules={[{ required: true, message: '请选择分发策略' }]}
        >
          <Select options={STRATEGY_OPTIONS} />
        </Form.Item>

        <Form.Item
          name="channelIds"
          label="目标渠道"
          rules={[{
            validator: (_, value: unknown) =>
              Array.isArray(value) && value.length > 0
                ? Promise.resolve()
                : Promise.reject(new Error('请至少选择一个目标渠道')),
          }]}
        >
          <Select
            mode="multiple"
            allowClear
            placeholder="从「渠道管理」中选择参与分发的渠道"
            options={opts}
            onChange={selectChannels}
            optionFilterProp="label"
          />
        </Form.Item>

        {strategy === 'weight' && (
          <Form.Item label="渠道权重（相对值，无需合计 100）" required>
            {channelIds.length === 0 ? (
              <div style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>
                请先在上方选择目标渠道。
              </div>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {channelIds.map(id => (
                  <div key={id} style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <span
                      style={{
                        flex: 1, fontSize: 13, overflow: 'hidden',
                        textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                      }}
                    >
                      {channelName(id)}
                    </span>
                    <InputNumber
                      min={1}
                      precision={0}
                      value={weights[id] ?? 100}
                      onChange={v => setWeights(w => ({ ...w, [id]: v ?? 100 }))}
                      addonAfter="权重"
                      style={{ width: 150 }}
                    />
                  </div>
                ))}
              </div>
            )}
          </Form.Item>
        )}

        <Form.Item name="fallbackChannelId" label="兜底渠道（全部目标渠道失败后使用）">
          <Select
            allowClear
            placeholder="选择兜底渠道"
            options={[{ value: NO_FALLBACK, label: '无兜底' }, ...opts]}
          />
        </Form.Item>

        <div style={{ display: 'flex', gap: 12 }}>
          <Form.Item
            name="retry"
            label="重试次数"
            rules={[{ required: true }]}
            style={{ flex: 1 }}
          >
            <InputNumber min={0} max={10} precision={0} style={{ width: '100%' }} addonAfter="次" />
          </Form.Item>
          <Form.Item
            name="timeoutMs"
            label="超时时间"
            tooltip="该规则命中的请求在单个候选上的等待上限"
            rules={[{ required: true }]}
            style={{ flex: 1 }}
          >
            <Select options={TIMEOUT_PRESETS.map(ms => ({ value: ms, label: timeoutLabel(ms) }))} />
          </Form.Item>
        </div>
      </Form>
    </Modal>
  );
}

export default function Routing() {
  const { message, modal } = App.useApp();
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
    modal.confirm({
      title: `删除规则「${r.name}」？`,
      content: '删除后该匹配将不再生效，其余规则顺序会自动重排。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => remove.mutateAsync(r.id).catch(() => undefined),
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

  const columns: ColumnsType<RouteRule> = [
    {
      title: '', width: 40,
      render: (_, __, i) => <span {...handleProps(i ?? 0)} aria-label="拖动调整顺序">⋮⋮</span>,
    },
    {
      title: '顺序', key: 'order', width: 64, align: 'right',
      render: (_, __, i) => <span className="gw-num" style={{ color: 'var(--gw-text-3)' }}>{i! + 1}</span>,
    },
    {
      title: '名称', dataIndex: 'name', width: 180,
      render: (v, r) => (
        <div>
          <div style={{ fontWeight: 500, color: r.enabled ? 'var(--gw-text)' : 'var(--gw-text-3)' }}>{v}</div>
          <div style={{ fontSize: 12.5, color: 'var(--gw-text-3)' }}>
            命中 <span className="gw-num">{fmt.n(r.hit)}</span> 次
          </div>
        </div>
      ),
    },
    {
      title: '启用', dataIndex: 'enabled', align: 'center', width: 80,
      render: (_, r) => (
        <Switch
          size="small"
          checked={r.enabled}
          loading={toggling.has(r.id)}
          disabled={toggling.has(r.id)}
          aria-label={`${r.enabled ? '停用' : '启用'}规则 ${r.name}`}
          onChange={c => toggle.mutate({ row: r, enabled: c })}
        />
      ),
    },
    {
      title: '匹配条件', key: 'match', width: 220,
      render: (_, r) => (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
          <span className="gw-badge">{MODE_TEXT[r.matchMode]}</span>
          <span className="gw-mono">{r.pattern}</span>
        </span>
      ),
    },
    {
      title: '分发策略', key: 'strategy', width: 220,
      render: (_, r) => (
        <div>
          <span className={`gw-badge${r.strategy === 'weight' ? ' tint' : ''}`}>{STRATEGY_TEXT[r.strategy]}</span>
          {r.strategy === 'weight' && r.weights && Object.keys(r.weights).length > 0 && (
            <div style={{ fontSize: 12.5, color: 'var(--gw-text-3)', marginTop: 5 }}>
              {Object.entries(r.weights)
                .map(([k, w]) => `${channelName(Number(k))} ${w}`)
                .join(' / ')}
            </div>
          )}
        </div>
      ),
    },
    {
      title: '目标渠道', key: 'channelIds', width: 220,
      render: (_, r) =>
        r.channelIds.length === 0 ? (
          <span style={{ color: 'var(--gw-text-3)' }}>—</span>
        ) : (
          <span style={{ display: 'inline-flex', gap: 5, flexWrap: 'wrap' }}>
            {r.channelIds.map(id => {
              const ch = channels.find(c => c.id === id);
              return (
                <span className="gw-badge" key={id} style={{ gap: 5 }}>
                  {ch && <ProviderMark name={ch.provider} size={14} />}
                  {ch?.name ?? `#${id}`}
                </span>
              );
            })}
          </span>
        ),
    },
    {
      title: '兜底', key: 'fallback', width: 120,
      render: (_, r) =>
        r.fallbackChannelId == null ? (
          <span style={{ color: 'var(--gw-text-3)' }}>无兜底</span>
        ) : (
          <span className="gw-badge" style={{ color: TOKENS.warn, borderColor: TOKENS.warn }}>
            {channelName(r.fallbackChannelId)}
          </span>
        ),
    },
    {
      title: '重试 / 超时', key: 'retryTimeout', width: 140,
      render: (_, r) => (
        <span style={{ fontSize: 13.5 }}>
          <span className="gw-num">{r.retry}</span> 次
          <span style={{ color: 'var(--gw-text-3)' }}> · {timeoutLabel(r.timeoutMs)}</span>
        </span>
      ),
    },
    {
      title: '操作', key: 'actions', align: 'right', width: 130,
      render: (_, r) => (
        <Space size={4}>
          <Button size="small" onClick={() => setEditor({ open: true, initial: r })}>编辑</Button>
          <Button size="small" danger onClick={() => confirmDelete(r)}>删除</Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="路由规则"
        desc="请求进来时按模型名匹配规则；规则优先级高于模型广场内部的供给源顺序"
        extra={
          <Button type="primary" onClick={() => setEditor({ open: true, initial: null })}>
            新建规则
          </Button>
        }
      />

      <Blocks>
        {/* 排序语义说明：拖拽是这一页的核心交互，必须显式写出来 */}
        <div className="gw-note" role="status">
          <b>自上而下匹配</b>
          <span>拖动左侧手柄调整顺序，越靠前优先级越高。未命中任何规则的请求将走默认渠道（供给源顺序）。</span>
        </div>

        <BlockCard>
          {isError ? (
            <ErrorState
              title="路由规则加载失败"
              desc="无法读取规则列表。未命中规则的请求仍按默认渠道转发。"
              onRetry={() => void refetch()}
            />
          ) : !isLoading && rules.length === 0 ? (
            <EmptyState
              title="还没有路由规则"
              desc="没有规则时，所有请求都走默认渠道（模型广场里的供给源顺序）。需要按模型名分流时再建规则。"
              action={<Button size="small" type="primary" onClick={() => setEditor({ open: true, initial: null })}>新建规则</Button>}
            />
          ) : (
            <Table<RouteRule>
              rowKey="id"
              size="middle"
              loading={isLoading && rules.length === 0}
              dataSource={rules}
              columns={columns}
              pagination={false}
              scroll={{ x: 1500 }}
              onRow={onRow}
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无规则" /> }}
            />
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
    </div>
  );
}
