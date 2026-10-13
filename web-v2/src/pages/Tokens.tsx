import { useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import StatusDot from '@/components/StatusDot';
import { EmptyState, ErrorState, NoResultState, SkBlock } from '@/components/States';
import {
  Badge, Button, CardBody, Checkbox, Copyable, Field, Input, Note, Seg, Select, Switch, Table,
} from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { Modal, confirm } from '@/ui/overlay';
import { message } from '@/ui/toast';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { copyText } from '@/utils/clipboard';
import { fmt } from '@/utils/format';
import { QUOTA_ALERT } from '@/utils/quota';
import type { GatewayToken, ProbeCheck, TokenCreateResult, TokenDraft, UserAccount } from '@/types';

const errMsg = (e: unknown) => (e instanceof Error ? e.message : '请稍后重试');

/** 表单值:日期在受控态里统一为 'YYYY-MM-DD' 字符串,提交时再决定是否下发。 */
interface TokenFormValues {
  name: string;
  enabled: boolean;
  allModels: boolean;
  modelNames: string[];
  quotaUsd: number;
  rpmLimit: number;
  expiry: 'never' | 'date';
  expiresDate: string;
  ownerId: number;
}

const DEFAULT_FORM: TokenFormValues = {
  name: '', enabled: true, allModels: true, modelNames: [], quotaUsd: 0,
  rpmLimit: 60, expiry: 'never', expiresDate: '', ownerId: 0,
};

/** 自绘覆盖层外壳:页内自管 open 的弹窗统一挂在这一层(去 antd 后不再走 OverlayHost)。 */
function Overlay({ children }: { children: ReactNode }) {
  return <div className="overlay" style={{ zIndex: 70 }}>{children}</div>;
}

/**
 * 新建/编辑令牌弹窗。允许模型走「允许全部 / 指定名单」二选一;管理员可指定归属用户。
 *
 * models 只用到名字:同一端点 /models 对管理员返回全量、对普通用户返回收敛清单,
 * 两者取 name 的写法一致,故这里收窄为 { name } 而不是 ModelCatalogItem。
 */
function TokenModal(props: {
  open: boolean;
  initial: GatewayToken | null;
  models: { name: string }[];
  isAdmin: boolean;
  users: UserAccount[];
  /** 普通用户建令牌的额度/RPM 上限(0 = 不限);由 /me/balance 带回 */
  ceiling: { quotaUsd: number; rpmLimit: number };
  onCancel: () => void;
  onSubmit: (draft: TokenDraft, id?: number) => Promise<void>;
}) {
  const { open, initial, models, isAdmin, users, ceiling, onCancel, onSubmit } = props;
  const [form, setForm] = useState<TokenFormValues>(DEFAULT_FORM);
  const [errors, setErrors] = useState<Partial<Record<keyof TokenFormValues, string>>>({});
  const [saving, setSaving] = useState(false);

  const set = <K extends keyof TokenFormValues>(k: K, v: TokenFormValues[K]) => {
    setForm(f => ({ ...f, [k]: v }));
    setErrors(e => (e[k] ? { ...e, [k]: undefined } : e));
  };

  // 打开时装载初值(编辑用行数据,新建用默认:全模型 + 永不过期)
  useEffect(() => {
    if (!open) return;
    if (initial) {
      const all = initial.allowedModels.length === 1 && initial.allowedModels[0] === '*';
      setErrors({});
      setForm({
        name: initial.name,
        enabled: initial.status !== 'disabled',
        allModels: all,
        modelNames: all ? [] : initial.allowedModels,
        quotaUsd: initial.quotaUsd,
        rpmLimit: initial.rpmLimit,
        expiry: initial.expiresAt ? 'date' : 'never',
        expiresDate: initial.expiresAt ? dayjs(initial.expiresAt).format('YYYY-MM-DD') : '',
        ownerId: initial.ownerId ?? 0,
      });
    } else {
      setErrors({});
      setForm(DEFAULT_FORM);
    }
  }, [open, initial]);

  // 名单模式候选:当前模型目录 + 该令牌已授权但已下架的模型名,避免编辑时丢失旧授权
  const modelOptions = useMemo(
    () => Array.from(new Set([
      ...models.map(m => m.name),
      ...(initial?.allowedModels ?? []).filter(x => x !== '*'),
    ])),
    [models, initial],
  );
  const addable = modelOptions.filter(n => !form.modelNames.includes(n));

  const submit = async () => {
    const next: Partial<Record<keyof TokenFormValues, string>> = {};
    if (!form.name.trim()) next.name = '请输入令牌名称';
    if (!form.allModels && form.modelNames.length === 0) next.modelNames = '请选择允许访问的模型，或勾选「允许全部模型」';
    if (form.expiry === 'date' && !form.expiresDate) next.expiresDate = '请选择到期日期';
    setErrors(next);
    if (Object.keys(next).length) return;

    const draft: TokenDraft = {
      name: form.name.trim(),
      allowedModels: form.allModels ? ['*'] : form.modelNames,
      quotaUsd: form.quotaUsd ?? 0,
      rpmLimit: form.rpmLimit ?? 60,
      expiresAt: form.expiry === 'never' ? null : form.expiresDate || null,
      status: form.enabled ? 'active' : 'disabled',
      // 归属仅创建时生效;user 由服务端强制为自己,不传
      ownerId: isAdmin ? (form.ownerId ? form.ownerId : null) : undefined,
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

  if (!open) return null;
  return (
    <Overlay>
      <Modal
        title={initial ? '编辑令牌' : '新建令牌'}
        width={560}
        onClose={onCancel}
        footer={
          <>
            <Button onClick={onCancel}>取消</Button>
            <Button variant="primary" loading={saving} onClick={submit}>{initial ? '保存' : '创建'}</Button>
          </>
        }
      >
        <div className="stack">
          <Switch checked={form.enabled} onChange={v => set('enabled', v)} label="启用该令牌" />

          <Field label="令牌名称" error={errors.name}>
            <Input
              placeholder="例如:CI 构建 / 本地 Claude Code"
              maxLength={64}
              invalid={!!errors.name}
              value={form.name}
              onChange={e => set('name', e.target.value)}
            />
          </Field>

          {isAdmin && (
            <Field label="归属用户" hint="仅创建时生效;选「全局」则不属于任何用户">
              <Select
                disabled={!!initial}
                value={String(form.ownerId)}
                onChange={e => set('ownerId', Number(e.target.value))}
                options={[
                  { value: 0, label: '全局(管理员)' },
                  ...users.map(u => ({ value: u.id, label: u.username })),
                ]}
              />
            </Field>
          )}

          <Field label="允许访问的模型" error={errors.modelNames}>
            <Checkbox checked={form.allModels} onChange={v => set('allModels', v)}>允许全部模型(含后续新上架模型)</Checkbox>
            {!form.allModels && (
              <div style={{ marginTop: 8 }}>
                <div className="taglist" style={{ marginBottom: 6 }}>
                  {form.modelNames.map(n => (
                    <span
                      key={n}
                      className="tag"
                      style={{ cursor: 'pointer' }}
                      title="点击移除"
                      onClick={() => set('modelNames', form.modelNames.filter(x => x !== n))}
                    >
                      {n} ×
                    </span>
                  ))}
                </div>
                <Select
                  value=""
                  disabled={addable.length === 0}
                  onChange={e => { if (e.target.value) set('modelNames', [...form.modelNames, e.target.value]); }}
                  options={[{ value: '', label: addable.length ? '添加模型…' : '已全部加入' }, ...addable.map(n => ({ value: n, label: n }))]}
                />
              </div>
            )}
          </Field>

          <div className="field-row">
            <Field
              label="额度上限"
              hint={
                !isAdmin && ceiling.quotaUsd > 0
                  ? `管理员限制你最高可设 ${fmt.usd(ceiling.quotaUsd)}(必须填正数)`
                  : '0 表示不限额；达到上限后网关将拒绝请求(HTTP 402)'
              }
            >
              <Input
                type="number"
                min={!isAdmin && ceiling.quotaUsd > 0 ? 0.01 : 0}
                step={0.1}
                placeholder="0 = 不限额"
                max={!isAdmin && ceiling.quotaUsd > 0 ? ceiling.quotaUsd : undefined}
                value={form.quotaUsd}
                onChange={e => set('quotaUsd', Number(e.target.value))}
              />
            </Field>
            <Field
              label="限速(RPM)"
              hint={
                !isAdmin && ceiling.rpmLimit > 0
                  ? `管理员限制你最高可设 ${ceiling.rpmLimit}(必须填正数)`
                  : '每分钟请求上限，0 表示不限速'
              }
            >
              <Input
                type="number"
                min={!isAdmin && ceiling.rpmLimit > 0 ? 1 : 0}
                max={!isAdmin && ceiling.rpmLimit > 0 ? ceiling.rpmLimit : 100000}
                placeholder="默认 60"
                value={form.rpmLimit}
                onChange={e => set('rpmLimit', Number(e.target.value))}
              />
            </Field>
          </div>

          <Field label="有效期">
            <Seg
              value={form.expiry}
              onChange={v => set('expiry', v)}
              options={[
                { value: 'never' as const, label: '永不过期' },
                { value: 'date' as const, label: '指定日期' },
              ]}
            />
          </Field>
          {form.expiry === 'date' && (
            <Field label="到期日期" error={errors.expiresDate}>
              <Input
                type="date"
                invalid={!!errors.expiresDate}
                min={dayjs().format('YYYY-MM-DD')}
                value={form.expiresDate}
                onChange={e => set('expiresDate', e.target.value)}
              />
            </Field>
          )}
        </div>
      </Modal>
    </Overlay>
  );
}

/** 「生成 Claude 配置」弹窗:拉取含真实 key 的 settings.json 片段,供逐字复制。 */
function ClaudeConfigModal(props: { token: GatewayToken | null; onClose: () => void }) {
  const { token, onClose } = props;
  const { data, error, isLoading } = useQuery({
    queryKey: ['claude-config', token?.id],
    queryFn: () => api.getClaudeConfig(token!.id),
    enabled: !!token,
    retry: false,
  });
  const err = error instanceof Error ? error.message : error ? '生成失败' : '';

  if (!token) return null;
  return (
    <Overlay>
      <Modal
        title={`Claude 配置 · ${token.name}`}
        width={640}
        onClose={onClose}
        footer={<Button variant="primary" onClick={onClose}>关闭</Button>}
      >
        <div className="stack">
          {err && (
            <Note tone="warn">
              <b>⚠ 生成失败</b>
              <div className="mono" style={{ marginTop: 4 }}>{err}</div>
            </Note>
          )}
          {data?.warnings?.map(w => (
            <Note key={w} tone="warn"><b>⚠ 注意</b>　{w}</Note>
          ))}
          <div style={{ fontSize: 13, color: 'var(--t2)' }}>
            把下面整段合并进 <span className="mono">~/.claude/settings.json</span> 的顶层(已有 <span className="mono">env</span> 则合并其键值),然后重启 Claude Code。
          </div>
          <div style={{ position: 'relative' }}>
            <div className="code" style={{ maxHeight: 360, overflow: 'auto' }}>
              {isLoading ? '生成中…' : (data?.settingsJson ?? '')}
            </div>
            <div style={{ position: 'absolute', top: 8, right: 8 }}>
              <Button
                size="sm"
                disabled={!data}
                onClick={() => data && void copyText(data.settingsJson).then(ok => (ok ? message.success('已复制') : message.warning('复制失败，请手动选择')))}
              >
                复制
              </Button>
            </div>
          </div>
        </div>
      </Modal>
    </Overlay>
  );
}

/** 「自检」弹窗:不产生真实调用地回答「这个 key 现在能不能用某模型」。 */
function ProbeModal(props: { token: GatewayToken | null; models: { name: string }[]; onClose: () => void }) {
  const { token, models, onClose } = props;
  const [model, setModel] = useState('');
  const { data, isFetching, isError, error, refetch } = useQuery({
    queryKey: ['token-probe', token?.id, model],
    queryFn: () => api.probeToken(token!.id, model),
    enabled: !!token && !!model,
    retry: false,
  });

  // 关闭时清掉模型选择,下次打开不残留上次结果。
  useEffect(() => {
    if (!token) setModel('');
  }, [token]);

  const checkColumns: Column<ProbeCheck>[] = [
    { key: 'name', title: '检查项', width: 110, render: c => <span style={{ color: 'var(--t2)' }}>{c.name}</span> },
    { key: 'ok', title: '结果', width: 90, render: c => <StatusDot status="" text={c.ok ? '通过' : '未通过'} tone={c.ok ? 'ok' : 'err'} /> },
    { key: 'detail', title: '说明', render: c => <span style={{ color: 'var(--t3)' }}>{c.detail || '—'}</span> },
  ];

  if (!token) return null;
  return (
    <Overlay>
      <Modal
        title={`自检 · ${token.name}`}
        width={560}
        onClose={onClose}
        footer={<Button variant="primary" onClick={onClose}>关闭</Button>}
      >
        <div className="stack">
          <Note>
            选择模型后核查「状态 / 有效期 / 授权 / 可用性 / 额度 / 余额」,<b>不访问上游、不计费、不消耗限速</b>。
          </Note>
          <Select
            value={model}
            onChange={e => setModel(e.target.value)}
            options={[{ value: '', label: '选择要自检的模型' }, ...models.map(m => ({ value: m.name, label: m.name }))]}
          />
          {!model ? (
            <div className="muted" style={{ fontSize: 13 }}>请先选择一个模型。</div>
          ) : isFetching ? (
            <SkBlock />
          ) : isError ? (
            <ErrorState
              title="自检失败"
              desc={(error as Error)?.message || '无法完成自检,请稍后重试。'}
              onRetry={() => void refetch()}
            />
          ) : data ? (
            <Table<ProbeCheck> columns={checkColumns} rows={data.checks} rowKey={c => c.name} />
          ) : null}
          {data && (
            <div style={{ fontSize: 13 }}>
              {data.ok
                ? <span style={{ color: 'var(--ok)' }}>✓ 该令牌可用「{data.model}」发起调用。</span>
                : <span style={{ color: 'var(--err)' }}>✗ 该令牌当前不能使用「{data.model}」,请按上表逐项处理。</span>}
            </div>
          )}
        </div>
      </Modal>
    </Overlay>
  );
}

export default function Tokens() {
  const qc = useQueryClient();
  const isAdmin = useSession(s => s.admin?.role) === 'admin';
  const [editor, setEditor] = useState<{ open: boolean; initial: GatewayToken | null }>({ open: false, initial: null });
  const [created, setCreated] = useState<TokenCreateResult | null>(null);
  const [configToken, setConfigToken] = useState<GatewayToken | null>(null);
  const [probeToken, setProbeToken] = useState<GatewayToken | null>(null);
  const [ownerFilter, setOwnerFilter] = useState<string>('all');

  const { data: tokens = [], isLoading, isError, refetch } = useQuery({ queryKey: ['tokens'], queryFn: api.getTokens });
  /*
   * 令牌的「允许模型」候选:按角色选端点。
   * /models 两种形状 —— 管理员是全量(含 offers),普通用户是收敛清单。
   * api.getModels() 会把返回值按 ModelCatalogItem 归一(reading offers),普通用户调用会抛。
   * 本页只用到模型名,故按角色各取各的;queryKey 区分,避免同键存两种形状互相覆盖。
   */
  const { data: models = [] } = useQuery({
    queryKey: isAdmin ? ['models'] : ['models', 'user'],
    queryFn: isAdmin ? api.getModels : api.getMyModels,
  });
  const { data: users = [] } = useQuery({ queryKey: ['users'], queryFn: api.getUsers, enabled: isAdmin });
  // 普通用户的钱包余额:令牌额度只是子预算,真正卡住调用的是余额(见 /me/balance)。
  const { data: wallet } = useQuery({ queryKey: ['me', 'balance'], queryFn: () => api.myBalance(1), enabled: !isAdmin });

  // 管理员可按归属过滤(数据量小,客户端过滤即可)
  const view = useMemo(
    () => (!isAdmin || ownerFilter === 'all'
      ? tokens
      : tokens.filter(t => (ownerFilter === '0' ? t.ownerId == null : t.ownerId === Number(ownerFilter)))),
    [tokens, isAdmin, ownerFilter],
  );

  const refresh = () => qc.invalidateQueries({ queryKey: ['tokens'] });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteToken(id),
    onSuccess: () => { message.success('已删除'); refresh(); },
    onError: e => message.error(`删除失败:${errMsg(e)}`),
  });

  const saveToken = async (draft: TokenDraft, id?: number) => {
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
  };

  const confirmDelete = (t: GatewayToken) => {
    void confirm({
      title: `删除令牌「${t.name}」?`,
      content: '删除后该 Key 立即失效；历史日志中的令牌名称仍会保留。',
      okText: '删除',
      cancelText: '取消',
      danger: true,
    }).then(ok => { if (ok) remove.mutate(t.id); });
  };

  /** 额度条：颜色随占比升级，但文字始终给出具体金额与百分比。 */
  const quotaCell = (r: GatewayToken) => {
    if (r.quotaUsd <= 0) {
      return <span className="num" style={{ fontSize: 13, color: 'var(--t3)' }}>{fmt.usd(r.usedUsd)} / 不限</span>;
    }
    const rate = Math.min(r.usedUsd / r.quotaUsd, 1);
    const warn = rate >= QUOTA_ALERT;
    return (
      <div>
        <div className="bar" role="img" aria-label={`额度已用 ${(rate * 100).toFixed(0)}%`} style={{ marginTop: 0 }}>
          <i style={{ width: `${rate * 100}%`, background: warn ? 'var(--warn)' : 'var(--brand)' }} />
        </div>
        <div className="num" style={{ fontSize: 12.5, color: 'var(--t3)', marginTop: 5 }}>
          {fmt.usd(r.usedUsd)} / {fmt.usd(r.quotaUsd)} · {(rate * 100).toFixed(0)}%
        </div>
      </div>
    );
  };

  const columns: Column<GatewayToken>[] = [
    {
      // 名称 / 归属 / Key 三合一 —— 都是「这个令牌是谁的、长什么样」
      key: 'name', title: '令牌', width: 240,
      render: (r) => (
        <div style={{ minWidth: 0, overflow: 'hidden' }}>
          <div className="hstack" style={{ gap: 8, minWidth: 0 }}>
            <b className="strong ellip" title={r.name} style={{ flex: '1 1 auto', minWidth: 0 }}>{r.name}</b>
            {isAdmin && (r.ownerId == null
              ? <span className="muted" style={{ fontSize: 12.5, flexShrink: 0 }}>全局</span>
              : <Badge tone="slate">{r.ownerName}</Badge>)}
          </div>
          <div className="mono" style={{ fontSize: 12, color: 'var(--t3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {r.keyMasked}
          </div>
        </div>
      ),
    },
    {
      // 模型名一律拼成一个字符串 + 单元格 ellipsis(Tooltip 给全文)。
      key: 'allowedModels', title: '可用模型', width: 140,
      render: (r) => {
        const list = r.allowedModels;
        if (list.length === 1 && list[0] === '*') return <Badge tone="brand-o">不限</Badge>;
        if (!list.length) return <span className="muted">无</span>;
        const text = list.join('、');
        return <span className="mono ellip" title={text}>{text}</span>;
      },
    },
    { key: 'quota', title: '额度使用', width: 158, render: r => quotaCell(r) },
    { key: 'rpmLimit', title: 'RPM', align: 'right', width: 72, render: r => <span className="num">{r.rpmLimit || '不限'}</span> },
    {
      key: 'expiresAt', title: '过期时间', width: 108,
      render: r => {
        if (!r.expiresAt) return <span className="muted">永不过期</span>;
        const d = dayjs(r.expiresAt);
        return <span className="num">{d.isValid() ? d.format('YYYY-MM-DD') : r.expiresAt}</span>;
      },
    },
    {
      key: 'lastUsedAt', title: '最后使用', width: 120,
      render: r => {
        if (!r.lastUsedAt) return <span className="muted">从未使用</span>;
        const d = dayjs(r.lastUsedAt);
        return <span className="num" style={{ color: 'var(--t3)' }}>{d.isValid() ? d.format('YYYY-MM-DD HH:mm') : r.lastUsedAt}</span>;
      },
    },
    { key: 'status', title: '状态', width: 78, render: r => <StatusDot status={r.status} /> },
    {
      // 高频（生成配置/编辑）外露，低频（自检/删除）也平铺为按钮 —— 自绘表格非 fixed 布局，
      // 行菜单的绝对定位会被 .table-wrap 的 overflow 裁切，故不再收进下拉。
      key: 'actions', title: '操作', align: 'right', width: 220,
      render: (r) => (
        <span className="hstack" style={{ gap: 4, justifyContent: 'flex-end' }}>
          <Button
            size="sm"
            disabled={!r.keyRetrievable}
            title={r.keyRetrievable ? undefined : '旧密钥无法回显，请重新创建'}
            onClick={() => setConfigToken(r)}
          >
            生成配置
          </Button>
          <Button size="sm" onClick={() => setEditor({ open: true, initial: r })}>编辑</Button>
          <Button size="sm" onClick={() => setProbeToken(r)}>自检</Button>
          <Button size="sm" variant="danger" onClick={() => confirmDelete(r)}>删除</Button>
        </span>
      ),
    },
  ];

  const emptyNode = isError ? (
    <ErrorState title="令牌列表加载失败" desc="无法读取令牌，对外分发的 Key 不受影响。" onRetry={() => void refetch()} />
  ) : tokens.length === 0 ? (
    <EmptyState
      title="还没有访问令牌"
      desc="令牌是对外分发的网关 Key。创建后可用它调用网关，并单独限制额度与速率。"
      action={<Button size="sm" variant="primary" onClick={() => setEditor({ open: true, initial: null })}>新建令牌</Button>}
    />
  ) : (
    <NoResultState title="没有符合归属筛选的令牌" desc="换一个归属用户，或切回「全部归属」。" />
  );

  return (
    <>
      <PageHeader
        title="访问令牌"
        desc="对外分发的网关 Key，可独立设额度、限流与过期时间"
        extra={
          <div className="hstack" style={{ gap: 6 }}>
            {isAdmin && (
              <Select
                value={ownerFilter}
                onChange={e => setOwnerFilter(e.target.value)}
                options={[
                  { value: 'all', label: '全部归属' },
                  { value: '0', label: '全局(管理员)' },
                  ...users.map(u => ({ value: u.id, label: u.username })),
                ]}
              />
            )}
            <Button variant="primary" onClick={() => setEditor({ open: true, initial: null })}>新建令牌</Button>
          </div>
        }
      />

      <Blocks>
        {!isAdmin && wallet && (
          <div className="hstack" style={{ gap: 10, fontSize: 13.5, color: 'var(--t2)', flexWrap: 'wrap' }}>
            <span className="muted">账户余额</span>
            <b className="num" style={{ color: wallet.balanceUsd <= 0 ? 'var(--err)' : 'var(--t1)' }}>{fmt.usd(wallet.balanceUsd)}</b>
            <span className="muted">
              {wallet.balanceUsd <= 0
                ? '余额不足，调用已被拒绝，请联系管理员充值'
                : '每次调用按本站售价从余额扣除；令牌额度为单令牌上限'}
            </span>
          </div>
        )}
        <BlockCard>
          {isLoading && tokens.length === 0 ? (
            <CardBody><SkBlock /></CardBody>
          ) : view.length === 0 ? (
            <CardBody>{emptyNode}</CardBody>
          ) : (
            <CardBody style={{ padding: 0 }}>
              <Table<GatewayToken> columns={columns} rows={view} rowKey={r => r.id} />
            </CardBody>
          )}
        </BlockCard>
      </Blocks>

      <TokenModal
        open={editor.open}
        initial={editor.initial}
        models={models}
        isAdmin={isAdmin}
        users={users}
        ceiling={{ quotaUsd: wallet?.tokenQuotaCeiling ?? 0, rpmLimit: wallet?.tokenRpmCeiling ?? 0 }}
        onCancel={() => setEditor({ open: false, initial: null })}
        onSubmit={saveToken}
      />

      <ClaudeConfigModal token={configToken} onClose={() => setConfigToken(null)} />
      <ProbeModal token={probeToken} models={models} onClose={() => setProbeToken(null)} />

      {created && (
        <Overlay>
          <Modal
            title="令牌已创建"
            onClose={() => setCreated(null)}
            footer={<Button variant="primary" onClick={() => setCreated(null)}>我已妥善保存</Button>}
          >
            <div className="stack">
              <Note>
                <b>密钥已加密存储</b>
                <div style={{ marginTop: 4 }}>之后可在列表用「生成配置」再次获取完整 Key。</div>
              </Note>
              <div style={{ fontSize: 13, color: 'var(--t2)' }}>
                掩码形式 <span className="mono" style={{ color: 'var(--t3)' }}>{created.token.keyMasked}</span>
              </div>
              <div>
                <div style={{ fontSize: 13, color: 'var(--t2)', marginBottom: 6 }}>完整 Key</div>
                <Copyable text={created.key}>
                  <span className="mono" style={{ wordBreak: 'break-all' }}>{created.key}</span>
                </Copyable>
              </div>
            </div>
          </Modal>
        </Overlay>
      )}
    </>
  );
}
