import { useMemo, useState } from 'react';
import {
  Alert, App, Button, Card, Col, Form, Input, InputNumber, Modal, Row, Select,
  Space, Spin, Switch, Table, Tooltip, Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';
import PageHeader from '@/components/PageHeader';
import ProviderMark from '@/components/ProviderMark';
import StatusTag from '@/components/StatusTag';
import { api } from '@/services/api';
import { providers } from '@/constants';
import { fmt } from '@/utils/format';
import type { Channel, ChannelDraft, ChannelQuota, HealthStatus, Provider, QuotaWindowKey } from '@/types';

const { Text } = Typography;

/** 新建渠道表单默认值 */
const DEFAULTS = {
  provider: 'OpenAI' as Provider,
  priority: 10,
  weight: 1,
  timeoutMs: 60000,
  enabled: true,
  maxFailures: 5,
  cooldownSec: 30,
  tags: [] as string[],
};

const TIMEOUT_OPTIONS = [
  { value: 5000, label: '5s' },
  { value: 10000, label: '10s' },
  { value: 30000, label: '30s' },
  { value: 60000, label: '60s' },
  { value: 120000, label: '2min' },
  { value: 300000, label: '5min' },
];

/** 渠道创建/编辑表单入参(apiKey 编辑态留空 = 不改) */
interface ChannelFormValues {
  name: string;
  provider: Provider;
  baseUrl: string;
  apiKey?: string;
  priority: number;
  weight: number;
  timeoutMs: number;
  enabled: boolean;
  maxFailures: number;
  cooldownSec: number;
  tags: string[];
  note?: string;
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : '操作失败,请稍后重试';
}

/** 额度窗口展示顺序与标签(rolling≈近5h)。 */
const QUOTA_WINS: Array<{ key: QuotaWindowKey; label: string }> = [
  { key: 'rolling', label: '5h' },
  { key: 'weekly', label: '周' },
  { key: 'monthly', label: '月' },
];

/** 百分比展示:整数不带小数,否则保留 1 位。 */
const pctText = (n: number): string => (Number.isInteger(n) ? String(n) : n.toFixed(1));

/** 已用阈值着色:≥80% 红 / ≥50% 橙。 */
const pctColor = (pct: number): string => (pct >= 80 ? '#EF4444' : pct >= 50 ? '#F59E0B' : 'inherit');

const dash = <span style={{ color: 'var(--gw-text-3)' }}>—</span>;

/** 额度单元格:可用→"5h 12% · 周 34% · 月 56%" + 逐窗口已用/剩余 tooltip;失败/不支持→灰色占位。 */
function QuotaCell({ q, provider }: { q: UseQueryResult<ChannelQuota, Error>; provider: Provider }) {
  if (provider === 'Anthropic') {
    return <Tooltip title="Anthropic 协议无 /v1/usage 额度接口">{dash}</Tooltip>;
  }
  if (q.isPending && !q.data) return <Spin size="small" />;
  const quota = q.data;
  if (q.isError || !quota || !quota.available) {
    return <Tooltip title={quota?.error || '额度接口未响应'}>{dash}</Tooltip>;
  }
  const wins = QUOTA_WINS.filter(w => quota.windows?.[w.key]?.status === 'ok');
  if (wins.length === 0) {
    return <Tooltip title="该渠道未返回可用额度窗口(不支持或已耗尽未上报)">{dash}</Tooltip>;
  }
  const detail = (
    <div style={{ fontSize: 12, lineHeight: 1.9, minWidth: 170 }}>
      {quota.planName && <div style={{ opacity: 0.85 }}>套餐：{quota.planName}</div>}
      {wins.map(w => {
        const pct = quota.windows![w.key]!.percent;
        return (
          <div key={w.key} style={{ display: 'flex', justifyContent: 'space-between', gap: 20 }}>
            <span>{w.label} 窗口</span>
            <span className="gw-num">已用 {pctText(pct)}% · 剩余 {pctText(Math.max(0, 100 - pct))}%</span>
          </div>
        );
      })}
    </div>
  );
  return (
    <Tooltip title={detail}>
      <span style={{ whiteSpace: 'nowrap' }} className="gw-num">
        {wins.map((w, i) => {
          const pct = quota.windows![w.key]!.percent;
          return (
            <span key={w.key}>
              {i > 0 && <span style={{ margin: '0 4px', color: 'var(--gw-text-3)' }}>·</span>}
              <span style={{ color: pctColor(pct) }}>{w.label} {pctText(pct)}%</span>
            </span>
          );
        })}
      </span>
    </Tooltip>
  );
}

export default function Channels() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<ChannelFormValues>();

  const [kw, setKw] = useState('');
  const [provider, setProvider] = useState('');
  const [status, setStatus] = useState('');
  const [testingId, setTestingId] = useState<number | null>(null);
  const [syncingId, setSyncingId] = useState<number | null>(null);

  // 新建 / 编辑共享弹窗
  const [editing, setEditing] = useState<Channel | null>(null);
  const [open, setOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  // 同步模型结果展示
  const [syncRes, setSyncRes] = useState<{ name: string; added: number; updated: number; models: string[]; modelCount: number } | null>(null);

  const { data: channels = [], isLoading } = useQuery({
    queryKey: ['channels'],
    queryFn: api.getChannels,
    retry: 0,
  });

  // 额度:对每条渠道并发查询上游 /v1/usage(Anthropic 协议渠道不查)。失败静默,UI 显示灰色占位。
  const quotaQueries = useQueries({
    queries: channels.map(ch => ({
      queryKey: ['channel-quota', ch.id],
      queryFn: () => api.channelQuota(ch.id),
      enabled: ch.provider !== 'Anthropic',
      retry: 0,
      staleTime: 60_000,
    })),
  });
  const quotaById = useMemo(() => {
    const m = new Map<number, UseQueryResult<ChannelQuota, Error>>();
    channels.forEach((ch, i) => m.set(ch.id, quotaQueries[i]));
    return m;
  }, [channels, quotaQueries]);

  const invalidate = () => qc.invalidateQueries({ queryKey: ['channels'] });

  const test = useMutation({
    mutationFn: (id: number) => api.testChannel(id),
    onSuccess: (r, id) => {
      setTestingId(null);
      const name = channels.find(c => c.id === id)?.name ?? '';
      if (r.ok) message.success(`${name} 探测成功 · ${fmt.ms(r.latencyMs)}`);
      else message.error(`${name} 探测失败:${r.message ?? '未知错误'}`);
      // 后端已把探测结果写回(清熔断 + EWMA 延迟),刷新以让行内/仪表盘展示一致。
      qc.invalidateQueries({ queryKey: ['channels'] });
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onError: () => {
      setTestingId(null);
      message.error('探测请求异常');
    },
  });

  // —— 新建 / 编辑 ——
  function openCreate() {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue(DEFAULTS);
    setOpen(true);
  }

  function openEdit(row: Channel) {
    setEditing(row);
    form.resetFields();
    form.setFieldsValue({
      name: row.name,
      provider: row.provider,
      baseUrl: row.baseUrl,
      priority: row.priority,
      weight: row.weight,
      timeoutMs: row.timeoutMs,
      enabled: row.enabled,
      maxFailures: row.maxFailures,
      cooldownSec: row.cooldownSec,
      tags: row.tags ?? [],
      note: row.note,
    });
    setOpen(true);
  }

  function closeModal() {
    setOpen(false);
    setEditing(null);
    form.resetFields();
  }

  async function handleSubmit(values: ChannelFormValues) {
    // updateChannel 是整体替换:必须从当前表单快照构造完整 draft。
    // apiKey 仅在显式填新值时下发(undefined / 留空则后端保持原密钥)。
    const base = {
      name: values.name.trim(),
      provider: values.provider,
      baseUrl: values.baseUrl.trim(),
      priority: values.priority ?? DEFAULTS.priority,
      weight: values.weight ?? DEFAULTS.weight,
      timeoutMs: values.timeoutMs ?? DEFAULTS.timeoutMs,
      enabled: values.enabled ?? DEFAULTS.enabled,
      maxFailures: values.maxFailures ?? DEFAULTS.maxFailures,
      cooldownSec: values.cooldownSec ?? DEFAULTS.cooldownSec,
      tags: values.tags ?? [],
      note: values.note?.trim() || undefined,
    };
    setSubmitting(true);
    try {
      if (editing) {
        const draft: ChannelDraft = { ...base, apiKey: values.apiKey?.trim() || undefined };
        await api.updateChannel(editing.id, draft);
        message.success(`渠道「${base.name}」已更新`);
      } else {
        const draft: ChannelDraft = { ...base, apiKey: values.apiKey?.trim() ?? '' };
        await api.createChannel(draft);
        message.success(`渠道「${base.name}」已创建`);
      }
      await invalidate();
      closeModal();
    } catch (e) {
      message.error(errText(e));
    } finally {
      setSubmitting(false);
    }
  }

  // —— 删除 ——
  function handleDelete(row: Channel) {
    modal.confirm({
      title: `删除渠道「${row.name}」?`,
      content: '将级联删除该渠道下的全部供给源与挂载关系,且不可恢复。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          await api.deleteChannel(row.id);
          message.success(`渠道「${row.name}」已删除`);
          await invalidate();
        } catch (e) {
          message.error(errText(e));
        }
      },
    });
  }

  // —— 同步模型(拉渠道 /v1/models → 补目录 + 挂停用 offer)——
  async function handleSyncModels(row: Channel) {
    setSyncingId(row.id);
    try {
      const r = await api.syncModels(row.id);
      setSyncRes({ name: row.name, added: r.added, updated: r.updated, models: r.models, modelCount: r.modelCount });
      // 弹窗总数取自后端同口径 modelCount;先把该行乐观对齐,再统一失效刷新,保证与列表同屏一致。
      qc.setQueryData<Channel[]>(['channels'], old =>
        (old ?? []).map(c => (c.id === row.id ? { ...c, modelCount: r.modelCount } : c)),
      );
      qc.invalidateQueries({ queryKey: ['channels'] });
      qc.invalidateQueries({ queryKey: ['models'] });
    } catch (e) {
      message.error(`同步失败:${errText(e)}`);
    } finally {
      setSyncingId(null);
    }
  }

  const list = channels.filter(c => {
    if (kw && !`${c.name}${c.baseUrl}`.toLowerCase().includes(kw.toLowerCase())) return false;
    if (provider && c.provider !== provider) return false;
    if (status && c.status !== status) return false;
    return true;
  });

  const columns: ColumnsType<Channel> = [
    {
      title: '名称', dataIndex: 'name',
      render: (v, r) => (
        <div>
          <div style={{ fontWeight: 500 }}>{v}</div>
          <div style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>{r.modelCount} 个模型</div>
        </div>
      ),
    },
    {
      title: '供应商', dataIndex: 'provider',
      render: v => (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <ProviderMark name={v} />{v}
        </span>
      ),
    },
    {
      title: 'Base URL', dataIndex: 'baseUrl',
      render: v => (
        <Text className="gw-mono" style={{ color: 'var(--gw-text-2)', maxWidth: 240 }} ellipsis>
          {v}
        </Text>
      ),
    },
    {
      title: '优先级', dataIndex: 'priority', align: 'right',
      render: v => <span className="gw-num">{v}</span>,
    },
    {
      title: '权重', dataIndex: 'weight', align: 'right',
      render: v => <span className="gw-num">{v}</span>,
    },
    {
      title: '成功率', dataIndex: 'successRate', align: 'right',
      render: v => <span className="gw-num">{fmt.pct(v, 2)}</span>,
    },
    {
      title: '延迟', dataIndex: 'latencyMs', align: 'right',
      render: (v, r) => (
        <span className="gw-num">{r.status === 'down' || r.status === 'disabled' ? '—' : fmt.ms(v)}</span>
      ),
    },
    {
      title: '今日 Tokens', dataIndex: 'todayTokens', align: 'right',
      render: v => <span className="gw-num">{fmt.k(v)}</span>,
    },
    {
      title: '今日花费', dataIndex: 'todayCostUsd', align: 'right',
      render: v => <span className="gw-num">{fmt.usd(v)}</span>,
    },
    {
      title: '额度', key: 'quota', width: 200,
      render: (_, r) => {
        const q = quotaById.get(r.id);
        return q ? <QuotaCell q={q} provider={r.provider} /> : dash;
      },
    },
    {
      title: '状态', dataIndex: 'status',
      render: (_, r) => (
        <Space size={6}>
          <StatusTag status={r.status} />
          {r.circuitOpen && <Text type="danger" style={{ fontSize: 12 }}>熔断中</Text>}
        </Space>
      ),
    },
    {
      title: '', align: 'right', width: 260,
      render: (_, r) => (
        <Space size={4} wrap>
          <Button
            size="small"
            loading={testingId === r.id}
            onClick={() => { setTestingId(r.id); test.mutate(r.id); }}
          >
            测试
          </Button>
          <Button size="small" loading={syncingId === r.id} onClick={() => handleSyncModels(r)}>
            同步模型
          </Button>
          <Button size="small" onClick={() => openEdit(r)}>编辑</Button>
          <Button size="small" danger onClick={() => handleDelete(r)}>删除</Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="渠道管理"
        desc="一条渠道 = 一个上游 API 端点与凭据,管的是「怎么连上去」"
        extra={<Button type="primary" onClick={openCreate}>新建渠道</Button>}
      />

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginBottom: 16 }}>
        <Input.Search allowClear placeholder="搜索名称或地址" style={{ width: 220 }} value={kw} onChange={e => setKw(e.target.value)} />
        <Select
          style={{ width: 140 }} value={provider} onChange={setProvider}
          options={[{ value: '', label: '全部供应商' }, ...providers.map(p => ({ value: p, label: p }))]}
        />
        <Select
          style={{ width: 130 }} value={status} onChange={setStatus}
          options={[
            { value: '', label: '全部状态' },
            { value: 'healthy' satisfies HealthStatus, label: '健康' },
            { value: 'degraded' satisfies HealthStatus, label: '降级' },
            { value: 'down' satisfies HealthStatus, label: '不可用' },
            { value: 'disabled' satisfies HealthStatus, label: '已停用' },
          ]}
        />
      </div>

      <Card>
        <Table<Channel>
          rowKey="id"
          size="middle"
          loading={isLoading}
          dataSource={list}
          columns={columns}
          scroll={{ x: 1480 }}
          pagination={{ pageSize: 10, showSizeChanger: false }}
        />
      </Card>

      {/* 新建 / 编辑共享弹窗 */}
      <Modal
        title={editing ? `编辑渠道「${editing.name}」` : '新建渠道'}
        open={open}
        onOk={() => form.submit()}
        confirmLoading={submitting}
        onCancel={closeModal}
        width={640}
        okText={editing ? '保存' : '创建'}
        cancelText="取消"
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={handleSubmit}
          initialValues={{ provider: DEFAULTS.provider, enabled: DEFAULTS.enabled }}
        >
          <Row gutter={12}>
            <Col span={12}>
              <Form.Item name="name" label="名称" rules={[{ required: true, whitespace: true, message: '请输入渠道名称' }]}>
                <Input placeholder="例:DeepSeek 官方" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="provider" label="供应商" rules={[{ required: true, message: '请选择供应商' }]}>
                <Select options={providers.map(p => ({ value: p, label: p }))} />
              </Form.Item>
            </Col>
          </Row>

          <Form.Item name="baseUrl" label="Base URL" rules={[{ required: true, whitespace: true, message: '请输入上游地址' }]}>
            <Input className="gw-mono" placeholder="https://api.deepseek.com/v1" />
          </Form.Item>

          <Form.Item
            name="apiKey"
            label="API Key"
            extra={editing ? '留空则保持原密钥不变' : undefined}
            rules={[{ required: !editing, whitespace: true, message: '请输入 API Key' }]}
          >
            <Input.Password
              autoComplete="new-password"
              placeholder={editing ? '留空则保持原密钥不变' : 'sk-…'}
            />
          </Form.Item>

          <Row gutter={12}>
            <Col span={8}>
              <Form.Item name="priority" label="优先级" tooltip="越小越优先参与选路" rules={[{ required: true, message: '必填' }]}>
                <InputNumber min={1} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="weight" label="权重" tooltip="按权重比例分摊流量(1-100)" rules={[{ required: true, message: '必填' }]}>
                <InputNumber min={1} max={100} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="timeoutMs" label="超时" rules={[{ required: true, message: '必填' }]}>
                <Select options={TIMEOUT_OPTIONS} />
              </Form.Item>
            </Col>
          </Row>

          <Row gutter={12}>
            <Col span={8}>
              <Form.Item name="maxFailures" label="熔断阈值(次)" tooltip="连续失败多少次后熔断" rules={[{ required: true, message: '必填' }]}>
                <InputNumber min={1} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="cooldownSec" label="熔断冷却(s)" tooltip="熔断后的冷却秒数" rules={[{ required: true, message: '必填' }]}>
                <InputNumber min={1} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="enabled" label="启用" valuePropName="checked" tooltip="停用后该渠道不再参与选路">
                <Switch />
              </Form.Item>
            </Col>
          </Row>

          <Form.Item name="tags" label="标签">
            <Select mode="tags" allowClear placeholder="回车添加,如:主力 / 备用 / 需配额" />
          </Form.Item>

          <Form.Item name="note" label="备注">
            <Input.TextArea rows={2} placeholder="可选" />
          </Form.Item>
        </Form>
      </Modal>

      {/* 同步模型结果 */}
      <Modal
        title={syncRes ? `同步模型 · ${syncRes.name}` : ''}
        open={!!syncRes}
        footer={null}
        onCancel={() => setSyncRes(null)}
        width={560}
      >
        {syncRes && (
          <>
            <Alert
              type={syncRes.added > 0 ? 'success' : 'info'}
              showIcon
              style={{ marginBottom: 12 }}
              message={`本渠道现关联 ${syncRes.modelCount} 个模型(本次新增 ${syncRes.added}、已存在 ${syncRes.updated})`}
              description="目录 / 供给源已刷新;新同步的模型默认停用,需到「模型广场」定价后启用。"
            />
            {syncRes.models.length > 0 ? (
              <div
                className="gw-mono"
                style={{
                  maxHeight: 320, overflow: 'auto', fontSize: 12, lineHeight: 1.8,
                  background: 'var(--gw-fill)', border: '1px solid var(--gw-border)',
                  borderRadius: 6, padding: '8px 12px',
                }}
              >
                {syncRes.models.join('\n')}
              </div>
            ) : (
              <Text type="secondary">该渠道没有返回新模型(目录中均已存在)。</Text>
            )}
          </>
        )}
      </Modal>
    </div>
  );
}
