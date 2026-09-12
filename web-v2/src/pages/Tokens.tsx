import { useEffect, useMemo, useState } from 'react';
import {
  App, Button, Checkbox, DatePicker, Form, Input, InputNumber, Modal,
  Radio, Select, Space, Switch, Table, Tooltip,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs, { type Dayjs } from 'dayjs';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import StatusDot from '@/components/StatusDot';
import { EmptyState, ErrorState, NoResultState } from '@/components/States';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { copyText } from '@/utils/clipboard';
import { fmt } from '@/utils/format';
import { TOKENS } from '@/styles/tokens';
import type { GatewayToken, ModelCatalogItem, TokenCreateResult, TokenDraft, UserAccount } from '@/types';

const errMsg = (e: unknown) => (e instanceof Error ? e.message : '请稍后重试');

/**
 * 额度阈值集中一处，避免各页各写一套。
 * 逼近=≥60%（概览「额度逼近」块用它），告警=≥85%（顶栏与状态条用它）。
 */
const QUOTA_ALERT = 0.85;

interface TokenFormValues {
  name: string;
  enabled: boolean;
  allModels: boolean;
  modelNames?: string[];
  quotaUsd: number;
  rpmLimit: number;
  expiry: 'never' | 'date';
  expiresDate?: Dayjs | null;
  ownerId?: number;
}

/** 新建/编辑令牌弹窗。允许模型走「允许全部 / 指定名单」二选一;管理员可指定归属用户。 */
function TokenModal(props: {
  open: boolean;
  initial: GatewayToken | null;
  models: ModelCatalogItem[];
  isAdmin: boolean;
  users: UserAccount[];
  onCancel: () => void;
  onSubmit: (draft: TokenDraft, id?: number) => Promise<void>;
}) {
  const { open, initial, models, isAdmin, users, onCancel, onSubmit } = props;
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
        ownerId: initial.ownerId ?? 0,
      });
    } else {
      form.resetFields();
      form.setFieldsValue({
        name: '', enabled: true, allModels: true, modelNames: [], quotaUsd: 0,
        rpmLimit: 60, expiry: 'never', expiresDate: null, ownerId: 0,
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
      // 归属仅创建时生效;user 由服务端强制为自己,不传
      ownerId: isAdmin ? (v.ownerId ? v.ownerId : null) : undefined,
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

        {isAdmin && (
          <Form.Item name="ownerId" label="归属用户" extra="仅创建时生效;选「全局」则不属于任何用户">
            <Select
              disabled={!!initial}
              options={[
                { value: 0, label: '全局(管理员)' },
                ...users.map(u => ({ value: u.id, label: u.username })),
              ]}
            />
          </Form.Item>
        )}

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

/** 「生成 Claude 配置」弹窗:拉取含真实 key 的 settings.json 片段,供逐字复制。 */
function ClaudeConfigModal(props: { token: GatewayToken | null; onClose: () => void }) {
  const { token, onClose } = props;
  const { message } = App.useApp();
  const { data, error, isLoading } = useQuery({
    queryKey: ['claude-config', token?.id],
    queryFn: () => api.getClaudeConfig(token!.id),
    enabled: !!token,
    retry: false,
  });
  const err = error instanceof Error ? error.message : error ? '生成失败' : '';

  const copy = (text: string) => {
    copyText(text).then(
      ok => (ok ? message.success('已复制') : message.warning('复制失败，请手动选择')),
    );
  };

  return (
    <Modal
      title={token ? `Claude 配置 · ${token.name}` : 'Claude 配置'}
      open={!!token}
      onCancel={onClose}
      footer={<Button type="primary" onClick={onClose}>关闭</Button>}
      destroyOnHidden
      width={640}
    >
      {err && (
        <div className="gw-note" role="status" style={{ borderLeftColor: TOKENS.err, background: 'var(--gw-card)', marginBottom: 12 }}>
          <b style={{ color: TOKENS.err }}>⚠ 生成失败</b>
          <span className="gw-mono" style={{ flex: 1 }}>{err}</span>
        </div>
      )}
      {data?.warnings?.map(w => (
        <div key={w} className="gw-note" role="status" style={{ borderLeftColor: TOKENS.warn, background: 'var(--gw-card)', marginBottom: 12 }}>
          <b style={{ color: TOKENS.warn }}>⚠ 注意</b>
          <span style={{ flex: 1 }}>{w}</span>
        </div>
      ))}
      <div style={{ fontSize: 13, color: 'var(--gw-text-2)', marginBottom: 8 }}>
        把下面整段合并进 <span className="gw-mono">~/.claude/settings.json</span> 的顶层(已有 <span className="gw-mono">env</span> 则合并其键值),然后重启 Claude Code。
      </div>
      <div style={{ position: 'relative' }}>
        <pre className="gw-pre" style={{ maxHeight: 360, overflow: 'auto' }}>
          {isLoading ? '生成中…' : (data?.settingsJson ?? '')}
        </pre>
        <Button
          size="small"
          style={{ position: 'absolute', top: 8, right: 8 }}
          disabled={!data}
          onClick={() => data && copy(data.settingsJson)}
        >
          复制
        </Button>
      </div>
    </Modal>
  );
}

export default function Tokens() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const isAdmin = useSession(s => s.admin?.role) === 'admin';
  const [editor, setEditor] = useState<{ open: boolean; initial: GatewayToken | null }>({ open: false, initial: null });
  const [created, setCreated] = useState<TokenCreateResult | null>(null);
  const [configToken, setConfigToken] = useState<GatewayToken | null>(null);
  const [ownerFilter, setOwnerFilter] = useState<number | 'all'>('all');

  const { data: tokens = [], isLoading, isError, refetch } = useQuery({ queryKey: ['tokens'], queryFn: api.getTokens });
  const { data: models = [] } = useQuery({ queryKey: ['models'], queryFn: api.getModels });
  const { data: users = [] } = useQuery({ queryKey: ['users'], queryFn: api.getUsers, enabled: isAdmin });

  // 管理员可按归属过滤(数据量小,客户端过滤即可)
  const view = useMemo(
    () => (!isAdmin || ownerFilter === 'all'
      ? tokens
      : tokens.filter(t => (ownerFilter === 0 ? t.ownerId == null : t.ownerId === ownerFilter))),
    [tokens, isAdmin, ownerFilter],
  );

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
    copyText(text).then(
      ok => (ok ? message.success('已复制') : message.warning('复制失败，请手动选择')),
    );
  };

  /** 额度条：颜色随占比升级，但文字始终给出具体金额与百分比。 */
  const quotaCell = (r: GatewayToken) => {
    if (r.quotaUsd <= 0) {
      return (
        <span className="gw-num" style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>
          {fmt.usd(r.usedUsd)} / 不限
        </span>
      );
    }
    const rate = Math.min(r.usedUsd / r.quotaUsd, 1);
    const tone = rate >= QUOTA_ALERT ? 'warn' : 'primary';
    return (
      <div>
        <div className="gw-bar" role="img" aria-label={`额度已用 ${(rate * 100).toFixed(0)}%`}>
          <i style={{ width: `${rate * 100}%`, background: tone === 'warn' ? TOKENS.warn : TOKENS.c1 }} />
        </div>
        <div className="gw-num" style={{ fontSize: 12.5, color: 'var(--gw-text-3)', marginTop: 5 }}>
          {fmt.usd(r.usedUsd)} / {fmt.usd(r.quotaUsd)} · {(rate * 100).toFixed(0)}%
        </div>
      </div>
    );
  };

  const columns: ColumnsType<GatewayToken> = useMemo(() => [
    { title: '名称', dataIndex: 'name', render: v => <b style={{ fontWeight: 500, color: 'var(--gw-text)' }}>{v}</b> },
    ...(isAdmin
      ? [{
          title: '归属', dataIndex: 'ownerName', width: 130,
          render: (_: unknown, r: GatewayToken) =>
            r.ownerId == null
              ? <span style={{ color: 'var(--gw-text-3)' }}>全局</span>
              : <span className="gw-badge">{r.ownerName}</span>,
        }] as ColumnsType<GatewayToken>
      : []),
    {
      title: 'Key', dataIndex: 'keyMasked', width: 200,
      render: v => <span className="gw-mono" style={{ color: 'var(--gw-text-3)' }}>{v}</span>,
    },
    {
      title: '可用模型', dataIndex: 'allowedModels', width: 200,
      render: v => {
        const list = v as string[];
        if (list.length === 1 && list[0] === '*') return <span className="gw-badge tint">不限</span>;
        if (!list.length) return <span style={{ color: 'var(--gw-text-3)' }}>无</span>;
        const shown = list.slice(0, 2);
        return (
          <span style={{ display: 'inline-flex', gap: 5, flexWrap: 'wrap' }}>
            {shown.map(m => <span className="gw-badge gw-mono" key={m}>{m}</span>)}
            {list.length > shown.length && (
              <Tooltip title={list.slice(2).join('、')}>
                <span className="gw-badge">+{list.length - shown.length}</span>
              </Tooltip>
            )}
          </span>
        );
      },
    },
    { title: '额度使用', key: 'quota', width: 210, render: (_, r) => quotaCell(r) },
    { title: 'RPM', dataIndex: 'rpmLimit', align: 'right', width: 90, render: v => <span className="gw-num">{v || '不限'}</span> },
    {
      title: '过期时间', dataIndex: 'expiresAt', width: 140,
      render: v => {
        if (!v) return <span style={{ color: 'var(--gw-text-3)' }}>永不过期</span>;
        const d = dayjs(v);
        if (!d.isValid()) return <span className="gw-mono">{v}</span>;
        return <span className="gw-num">{d.format('YYYY-MM-DD')}</span>;
      },
    },
    {
      title: '最后使用', dataIndex: 'lastUsedAt', width: 160,
      render: v => {
        if (!v) return <span style={{ color: 'var(--gw-text-3)' }}>从未使用</span>;
        const d = dayjs(v);
        return <span className="gw-num" style={{ color: 'var(--gw-text-3)' }}>{d.isValid() ? d.format('YYYY-MM-DD HH:mm') : v}</span>;
      },
    },
    { title: '状态', dataIndex: 'status', width: 100, render: v => <StatusDot status={v} /> },
    {
      title: '操作', align: 'right', width: 210,
      render: (_, r) => (
        <Space size={4}>
          <Tooltip title={r.keyRetrievable ? undefined : '旧密钥无法回显，请重新创建'}>
            <Button size="small" disabled={!r.keyRetrievable} onClick={() => setConfigToken(r)}>
              生成配置
            </Button>
          </Tooltip>
          <Button size="small" onClick={() => setEditor({ open: true, initial: r })}>编辑</Button>
          <Button size="small" danger onClick={() => confirmDelete(r)}>删除</Button>
        </Space>
      ),
    },
  ], [isAdmin]); // eslint-disable-line react-hooks/exhaustive-deps

  const emptyNode = isError ? (
    <ErrorState title="令牌列表加载失败" desc="无法读取令牌，对外分发的 Key 不受影响。" onRetry={() => void refetch()} />
  ) : tokens.length === 0 ? (
    <EmptyState
      title="还没有访问令牌"
      desc="令牌是对外分发的网关 Key。创建后可用它调用网关，并单独限制额度与速率。"
      action={<Button size="small" type="primary" onClick={() => setEditor({ open: true, initial: null })}>新建令牌</Button>}
    />
  ) : (
    <NoResultState title="没有符合归属筛选的令牌" desc="换一个归属用户，或切回「全部归属」。" />
  );

  return (
    <div className="gw-page">
      <PageHeader
        title="访问令牌"
        desc="对外分发的网关 Key，可独立设额度、限流与过期时间"
        extra={
          <Space>
            {isAdmin && (
              <Select
                value={ownerFilter}
                style={{ width: 180 }}
                onChange={setOwnerFilter}
                options={[
                  { value: 'all', label: '全部归属' },
                  { value: 0, label: '全局(管理员)' },
                  ...users.map(u => ({ value: u.id, label: u.username })),
                ]}
              />
            )}
            <Button type="primary" onClick={() => setEditor({ open: true, initial: null })}>新建令牌</Button>
          </Space>
        }
      />

      <Blocks>
        <BlockCard>
          <Table<GatewayToken>
            rowKey="id"
            size="middle"
            loading={isLoading && tokens.length === 0}
            dataSource={view}
            columns={columns}
            scroll={{ x: 1420 }}
            pagination={false}
            locale={{ emptyText: emptyNode }}
          />
        </BlockCard>
      </Blocks>

      <TokenModal
        open={editor.open}
        initial={editor.initial}
        models={models}
        isAdmin={isAdmin}
        users={users}
        onCancel={() => setEditor({ open: false, initial: null })}
        onSubmit={saveToken}
      />

      <ClaudeConfigModal token={configToken} onClose={() => setConfigToken(null)} />

      <Modal
        open={!!created}
        title="令牌已创建"
        onCancel={() => setCreated(null)}
        footer={<Button type="primary" onClick={() => setCreated(null)}>我已妥善保存</Button>}
      >
        {created && (
          <>
            <div className="gw-note" role="status" style={{ marginBottom: 16 }}>
              <b>密钥已加密存储</b>
              <span>之后可在列表用「生成配置」再次获取完整 Key。</span>
            </div>
            <div style={{ fontSize: 13, color: 'var(--gw-text-2)', marginBottom: 14 }}>
              掩码形式
              <span className="gw-mono" style={{ marginLeft: 8, color: 'var(--gw-text-3)' }}>{created.token.keyMasked}</span>
            </div>
            <div style={{ fontSize: 13, color: 'var(--gw-text-2)', marginBottom: 6 }}>完整 Key</div>
            <div
              style={{
                display: 'flex', alignItems: 'center', gap: 8, wordBreak: 'break-all',
                fontSize: 13,
                border: '1px solid var(--gw-border)', borderRadius: 'var(--gw-r-card)', padding: 12,
                background: 'var(--gw-bg)',
              }}
            >
              <span className="gw-mono">{created.key}</span>
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
