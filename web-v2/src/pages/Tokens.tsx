import { useEffect, useState } from 'react';
import {
  App, Button, Card, Checkbox, DatePicker, Form, Input, InputNumber, Modal,
  Radio, Select, Space, Switch, Table, Tag,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs, { type Dayjs } from 'dayjs';
import PageHeader from '@/components/PageHeader';
import StatusTag from '@/components/StatusTag';
import { api } from '@/services/api';
import { fmt } from '@/utils/format';
import type { GatewayToken, ModelCatalogItem, TokenCreateResult, TokenDraft } from '@/types';

const errMsg = (e: unknown) => (e instanceof Error ? e.message : '请稍后重试');

interface TokenFormValues {
  name: string;
  enabled: boolean;
  allModels: boolean;
  modelNames?: string[];
  quotaUsd: number;
  rpmLimit: number;
  expiry: 'never' | 'date';
  expiresDate?: Dayjs | null;
}

/** 新建/编辑令牌弹窗。允许模型走「允许全部 / 指定名单」二选一。 */
function TokenModal(props: {
  open: boolean;
  initial: GatewayToken | null;
  models: ModelCatalogItem[];
  onCancel: () => void;
  onSubmit: (draft: TokenDraft, id?: number) => Promise<void>;
}) {
  const { open, initial, models, onCancel, onSubmit } = props;
  const { message } = App.useApp();
  const [form] = Form.useForm<TokenFormValues>();
  const [saving, setSaving] = useState(false);

  const allModels = Form.useWatch('allModels', form) ?? true;
  const expiry = Form.useWatch('expiry', form) ?? 'never';

  // 打开时装载初值(编辑用行数据,新建用默认:全模型 + 永不过期)
  useEffect(() => {
    if (!open) return;
    if (initial) {
      const all = initial.allowedModels.length === 1 && initial.allowedModels[0] === '*';
      form.setFieldsValue({
        name: initial.name,
        enabled: initial.status !== 'disabled',
        allModels: all,
        modelNames: all ? [] : initial.allowedModels,
        quotaUsd: initial.quotaUsd,
        rpmLimit: initial.rpmLimit,
        expiry: initial.expiresAt ? 'date' : 'never',
        expiresDate: initial.expiresAt ? dayjs(initial.expiresAt) : null,
      });
    } else {
      form.resetFields();
      form.setFieldsValue({
        name: '', enabled: true, allModels: true, modelNames: [], quotaUsd: 0,
        rpmLimit: 60, expiry: 'never', expiresDate: null,
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initial, form]);

  // 名单模式候选:当前模型目录 + 该令牌已授权但已下架的模型名,避免编辑时丢失旧授权
  const modelOptions = Array.from(new Set([
    ...models.map(m => m.name),
    ...(initial?.allowedModels ?? []).filter(x => x !== '*'),
  ])).map(name => ({ value: name, label: name }));

  const submit = async () => {
    const v = await form.validateFields();
    if (!v.allModels && (!v.modelNames || v.modelNames.length === 0)) {
      message.warning('请选择允许访问的模型，或勾选「允许全部模型」');
      return;
    }
    const draft: TokenDraft = {
      name: v.name.trim(),
      allowedModels: v.allModels ? ['*'] : (v.modelNames ?? []),
      quotaUsd: v.quotaUsd ?? 0,
      rpmLimit: v.rpmLimit ?? 60,
      expiresAt: v.expiry === 'never' ? null
        : (v.expiresDate ? v.expiresDate.format('YYYY-MM-DD') : null),
      status: v.enabled ? 'active' : 'disabled',
    };
    setSaving(true);
    try {
      await onSubmit(draft, initial?.id);
    } catch (e) {
      message.error(`保存失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={initial ? '编辑令牌' : '新建令牌'}
      open={open}
      onCancel={onCancel}
      onOk={submit}
      confirmLoading={saving}
      okText={initial ? '保存' : '创建'}
      cancelText="取消"
      destroyOnHidden
      width={560}
    >
      <Form form={form} layout="vertical" requiredMark={false} preserve={false}>
        <Space size={10} align="center" style={{ marginBottom: 18 }}>
          <Form.Item name="enabled" valuePropName="checked" noStyle>
            <Switch />
          </Form.Item>
          <span style={{ fontSize: 13, color: 'var(--gw-text-2)' }}>启用该令牌</span>
        </Space>

        <Form.Item
          name="name" label="令牌名称"
          rules={[{ required: true, whitespace: true, message: '请输入令牌名称' }]}
        >
          <Input placeholder="例如:CI 构建 / 本地 Claude Code" maxLength={64} />
        </Form.Item>

        <Form.Item label="允许访问的模型">
          <Form.Item name="allModels" valuePropName="checked" noStyle>
            <Checkbox>允许全部模型(含后续新上架模型)</Checkbox>
          </Form.Item>
          {!allModels && (
            <Form.Item name="modelNames" noStyle>
              <Select
                mode="multiple"
                allowClear
                showSearch
                placeholder="指定允许的模型(可多选)"
                style={{ marginTop: 8, width: '100%' }}
                options={modelOptions}
                optionFilterProp="label"
              />
            </Form.Item>
          )}
        </Form.Item>

        <Space size={16} style={{ display: 'flex' }} align="start">
          <Form.Item
            name="quotaUsd" label="额度上限(USD)"
            extra="0 表示不限额；达到上限后网关将拒绝请求(HTTP 402)"
            style={{ flex: 1 }}
          >
            <InputNumber min={0} step={0.1} precision={2} style={{ width: '100%' }} placeholder="0 = 不限额" />
          </Form.Item>
          <Form.Item
            name="rpmLimit" label="限速(RPM)"
            extra="每分钟请求上限，0 表示不限速"
            style={{ flex: 1 }}
          >
            <InputNumber min={0} max={100000} style={{ width: '100%' }} placeholder="默认 60" />
          </Form.Item>
        </Space>

        <Form.Item name="expiry" label="有效期">
          <Radio.Group
            options={[
              { value: 'never', label: '永不过期' },
              { value: 'date', label: '指定日期' },
            ]}
          />
        </Form.Item>
        {expiry === 'date' && (
          <Form.Item
            name="expiresDate" label="到期日期"
            rules={[{ required: true, message: '请选择到期日期' }]}
          >
            <DatePicker
              style={{ width: '100%' }}
              format="YYYY-MM-DD"
              disabledDate={d => !!d && d.isBefore(dayjs().startOf('day'))}
            />
          </Form.Item>
        )}
      </Form>
    </Modal>
  );
}

export default function Tokens() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const [editor, setEditor] = useState<{ open: boolean; initial: GatewayToken | null }>({ open: false, initial: null });
  const [created, setCreated] = useState<TokenCreateResult | null>(null);

  const { data: tokens = [], isLoading } = useQuery({ queryKey: ['tokens'], queryFn: api.getTokens });
  const { data: models = [] } = useQuery({ queryKey: ['models'], queryFn: api.getModels });

  const refresh = () => qc.invalidateQueries({ queryKey: ['tokens'] });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteToken(id),
    onSuccess: () => { message.success('已删除'); refresh(); },
    onError: e => message.error(`删除失败:${errMsg(e)}`),
  });

  const saveToken = async (draft: TokenDraft, id?: number) => {
    try {
      if (id != null) {
        await api.updateToken(id, draft);
        message.success('令牌已保存');
      } else {
        const r = await api.createToken(draft);
        setCreated(r);
        message.success('令牌已创建');
      }
      refresh();
      setEditor({ open: false, initial: null });
    } catch (e) {
      message.error(`保存失败:${errMsg(e)}`);
    }
  };

  const confirmDelete = (t: GatewayToken) => {
    modal.confirm({
      title: `删除令牌「${t.name}」?`,
      content: '删除后该 Key 立即失效；历史日志中的令牌名称仍会保留。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => remove.mutateAsync(t.id),
    });
  };

  const copyKey = (text: string) => {
    navigator.clipboard?.writeText(text).then(
      () => message.success('已复制'),
      () => message.warning('复制失败，请手动选择'),
    );
  };

  const columns: ColumnsType<GatewayToken> = [
    { title: '名称', dataIndex: 'name', render: v => <b style={{ fontWeight: 500 }}>{v}</b> },
    {
      title: 'Key', dataIndex: 'keyMasked', width: 200,
      render: v => (
        <span
          className="gw-mono"
          style={{
            fontSize: 12, padding: '1px 8px', borderRadius: 4,
            border: '1px solid var(--gw-border)', background: 'var(--gw-fill)',
            color: 'var(--gw-text-2)',
          }}
        >
          {v}
        </span>
      ),
    },
    {
      title: '可用模型', dataIndex: 'allowedModels', width: 220,
      render: v => {
        const list = v as string[];
        if (list.length === 1 && list[0] === '*') return <Tag>不限</Tag>;
        if (!list.length) return <span style={{ color: 'var(--gw-text-3)' }}>无</span>;
        return list.map(m => <Tag key={m} style={{ marginInlineEnd: 4 }}>{m}</Tag>);
      },
    },
    {
      title: '额度使用', key: 'quota', width: 220,
      render: (_, r) => {
        if (r.quotaUsd <= 0) {
          return (
            <span style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
              已用 <span className="gw-num">{fmt.usd(r.usedUsd)}</span> / 不限
            </span>
          );
        }
        const rate = Math.min(r.usedUsd / r.quotaUsd, 1);
        const color = rate > 0.9 ? '#EF4444' : rate > 0.8 ? '#F59E0B' : 'var(--gw-primary)';
        return (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <div style={{ flex: 1, height: 4, borderRadius: 2, background: 'var(--gw-fill)', overflow: 'hidden' }}>
              <div style={{ height: '100%', width: `${rate * 100}%`, background: color, borderRadius: 2 }} />
            </div>
            <span className="gw-num" style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
              {fmt.usd(r.usedUsd)} / {fmt.usd(r.quotaUsd)}
            </span>
          </div>
        );
      },
    },
    {
      title: 'RPM', dataIndex: 'rpmLimit', align: 'right',
      render: v => <span className="gw-num">{v}</span>,
    },
    {
      title: '过期时间', dataIndex: 'expiresAt', width: 140,
      render: v => {
        if (!v) return <span style={{ color: 'var(--gw-text-3)' }}>永不过期</span>;
        const d = dayjs(v);
        if (!d.isValid()) return <span className="gw-mono">{v}</span>;
        const soon = d.isBefore(dayjs().add(7, 'day'));
        return (
          <span className="gw-mono" style={{ color: soon ? (d.isBefore(dayjs()) ? '#EF4444' : '#F59E0B') : undefined }}>
            {d.format('YYYY-MM-DD')}
          </span>
        );
      },
    },
    {
      title: '最后使用', dataIndex: 'lastUsedAt', width: 150,
      render: v => {
        if (!v) return <span style={{ color: 'var(--gw-text-3)' }}>从未使用</span>;
        const d = dayjs(v);
        return <span style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>{d.isValid() ? d.format('YYYY-MM-DD HH:mm') : v}</span>;
      },
    },
    {
      title: '状态', dataIndex: 'status', width: 90,
      render: v => <StatusTag status={v} />,
    },
    {
      title: '', align: 'right', width: 120,
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
        title="访问令牌"
        desc="对外分发的网关 Key，可独立设额度、限流与过期时间"
        extra={
          <Button type="primary" onClick={() => setEditor({ open: true, initial: null })}>
            新建令牌
          </Button>
        }
      />

      <Card>
        <Table<GatewayToken>
          rowKey="id"
          size="middle"
          loading={isLoading}
          dataSource={tokens}
          columns={columns}
          scroll={{ x: 1320 }}
          pagination={false}
        />
      </Card>

      <TokenModal
        open={editor.open}
        initial={editor.initial}
        models={models}
        onCancel={() => setEditor({ open: false, initial: null })}
        onSubmit={saveToken}
      />

      <Modal
        open={!!created}
        title="令牌已创建"
        onCancel={() => setCreated(null)}
        footer={<Button type="primary" onClick={() => setCreated(null)}>我已妥善保存</Button>}
      >
        {created && (
          <>
            <div
              style={{
                border: '1px solid var(--gw-border)', borderLeft: '2px solid #F59E0B',
                borderRadius: 6, padding: '10px 12px', fontSize: 13,
                color: 'var(--gw-text-2)', background: 'var(--gw-fill)', marginBottom: 16,
              }}
            >
              密钥只显示一次，关闭后无法再次查看完整 Key，请妥善保存。
            </div>
            <div style={{ fontSize: 13, color: 'var(--gw-text-2)', marginBottom: 6 }}>
              掩码形式
              <span className="gw-mono" style={{ marginLeft: 8, color: 'var(--gw-text-3)' }}>{created.token.keyMasked}</span>
            </div>
            <div style={{ fontSize: 13, color: 'var(--gw-text-2)', marginBottom: 6 }}>完整 Key</div>
            <div
              style={{
                display: 'flex', alignItems: 'center', gap: 8, wordBreak: 'break-all',
                fontFamily: 'ui-monospace, Menlo, Consolas, monospace', fontSize: 13,
                border: '1px solid var(--gw-border)', borderRadius: 8, padding: 12,
                background: 'var(--gw-fill)',
              }}
            >
              {created.key}
              <Button size="small" style={{ marginLeft: 'auto', flex: '0 0 auto' }} onClick={() => copyKey(created.key)}>
                复制
              </Button>
            </div>
          </>
        )}
      </Modal>
    </div>
  );
}
