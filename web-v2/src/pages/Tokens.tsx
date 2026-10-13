import { useEffect, useMemo, useState } from 'react';
import {
  Badge, Btn, Check, Copyable, Field, IconBtn, Input, Seg, Select, Switch,
} from '@/ui/primitives';
import { Dropdown, Modal, confirm } from '@/ui/overlay';
import { toast } from '@/ui/toast';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import StatusDot from '@/components/StatusDot';
import { EmptyState, ErrorState, NoResultState, SkBlock } from '@/components/States';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { copyText } from '@/utils/clipboard';
import { fmt } from '@/utils/format';
import { QUOTA_ALERT } from '@/utils/quota';
import type { GatewayToken, TokenCreateResult, TokenDraft, UserAccount } from '@/types';

const errMsg = (e: unknown) => (e instanceof Error ? e.message : '请稍后重试');

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
  const [saving, setSaving] = useState(false);

  const [enabled, setEnabled] = useState(true);
  const [name, setName] = useState('');
  const [ownerId, setOwnerId] = useState(0);
  const [allModels, setAllModels] = useState(true);
  const [modelNames, setModelNames] = useState<string[]>([]);
  const [quotaUsd, setQuotaUsd] = useState(0);
  const [rpmLimit, setRpmLimit] = useState(60);
  const [expiry, setExpiry] = useState<'never' | 'date'>('never');
  const [expiresDate, setExpiresDate] = useState<string>('');

  // 打开时装载初值(编辑用行数据,新建用默认:全模型 + 永不过期)
  useEffect(() => {
    if (!open) return;
    if (initial) {
      const all = initial.allowedModels.length === 1 && initial.allowedModels[0] === '*';
      setName(initial.name);
      setEnabled(initial.status !== 'disabled');
      setAllModels(all);
      setModelNames(all ? [] : initial.allowedModels);
      setQuotaUsd(initial.quotaUsd);
      setRpmLimit(initial.rpmLimit);
      setExpiry(initial.expiresAt ? 'date' : 'never');
      setExpiresDate(initial.expiresAt ? String(initial.expiresAt).slice(0, 10) : '');
      setOwnerId(initial.ownerId ?? 0);
    } else {
      setName(''); setEnabled(true); setAllModels(true); setModelNames([]);
      setQuotaUsd(0); setRpmLimit(60); setExpiry('never'); setExpiresDate(''); setOwnerId(0);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initial]);

  // 名单模式候选:当前模型目录 + 该令牌已授权但已下架的模型名,避免编辑时丢失旧授权
  const modelOptions = Array.from(new Set([
    ...models.map(m => m.name),
    ...(initial?.allowedModels ?? []).filter(x => x !== '*'),
  ]));

  const submit = async () => {
    if (!name.trim()) { toast.err('请输入令牌名称'); return; }
    if (!allModels && (!modelNames || modelNames.length === 0)) {
      toast.info('请选择允许访问的模型，或勾选「允许全部模型」');
      return;
    }
    if (expiry === 'date' && !expiresDate) { toast.err('请选择到期日期'); return; }
    const draft: TokenDraft = {
      name: name.trim(),
      allowedModels: allModels ? ['*'] : (modelNames ?? []),
      quotaUsd: quotaUsd ?? 0,
      rpmLimit: rpmLimit ?? 60,
      expiresAt: expiry === 'never' ? null : (expiresDate || null),
      status: enabled ? 'active' : 'disabled',
      // 归属仅创建时生效;user 由服务端强制为自己,不传
      ownerId: isAdmin ? (ownerId ? ownerId : null) : undefined,
    };
    setSaving(true);
    try {
      await onSubmit(draft, initial?.id);
    } catch (e) {
      toast.err(`保存失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={initial ? '编辑令牌' : '新建令牌'}
      open={open}
      onClose={onCancel}
      width={560}
      footer={
        <>
          <Btn onClick={onCancel}>取消</Btn>
          <Btn variant="primary" loading={saving} onClick={submit}>{initial ? '保存' : '创建'}</Btn>
        </>
      }
    >
      <label className="hstack" style={{ marginBottom: 18, cursor: 'pointer', fontSize: 13, color: 'var(--t2)' }}>
        <Switch checked={enabled} onChange={setEnabled} />
        <span>启用该令牌</span>
      </label>

      <Field label="令牌名称">
        <Input placeholder="例如:CI 构建 / 本地 Claude Code" maxLength={64} value={name} onChange={e => setName(e.target.value)} />
      </Field>

      {isAdmin && (
        <Field label="归属用户" hint="仅创建时生效;选「全局」则不属于任何用户">
          <Select
            disabled={!!initial}
            value={ownerId}
            onChange={e => setOwnerId(Number(e.target.value))}
          >
            <option value={0}>全局(管理员)</option>
            {users.map(u => <option key={u.id} value={u.id}>{u.username}</option>)}
          </Select>
        </Field>
      )}

      <Field label="允许访问的模型">
        <Check checked={allModels} onChange={setAllModels}>允许全部模型(含后续新上架模型)</Check>
        {!allModels && (
          <Select
            multiple
            value={modelNames}
            onChange={e => setModelNames(Array.from(e.target.selectedOptions).map(o => o.value))}
            style={{ marginTop: 8, height: 140 }}
          >
            {modelOptions.map(name => <option key={name} value={name}>{name}</option>)}
          </Select>
        )}
      </Field>

      <div className="form-grid">
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
            value={quotaUsd}
            onChange={e => setQuotaUsd(Number(e.target.value))}
            placeholder="0 = 不限额"
            max={!isAdmin && ceiling.quotaUsd > 0 ? ceiling.quotaUsd : undefined}
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
            value={rpmLimit}
            onChange={e => setRpmLimit(Number(e.target.value))}
            placeholder="默认 60"
          />
        </Field>
      </div>

      <Field label="有效期">
        <Seg
          value={expiry}
          onChange={v => setExpiry(v as 'never' | 'date')}
          options={[{ v: 'never', label: '永不过期' }, { v: 'date', label: '指定日期' }]}
        />
      </Field>
      {expiry === 'date' && (
        <Field label="到期日期">
          <Input type="date" value={expiresDate} onChange={e => setExpiresDate(e.target.value)} />
        </Field>
      )}
    </Modal>
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

  const copy = (text: string) => {
    copyText(text).then(ok => (ok ? toast.ok('已复制') : toast.info('复制失败，请手动选择')));
  };

  return (
    <Modal
      title={token ? `Claude 配置 · ${token.name}` : 'Claude 配置'}
      open={!!token}
      onClose={onClose}
      width={640}
      footer={<Btn variant="primary" onClick={onClose}>关闭</Btn>}
    >
      {err && (
        <div className="note note-warn" role="status" style={{ borderLeftColor: 'var(--err)', marginBottom: 12 }}>
          <b style={{ color: 'var(--err)' }}>⚠ 生成失败</b>
          <span className="mono" style={{ flex: 1 }}>{err}</span>
        </div>
      )}
      {data?.warnings?.map(w => (
        <div key={w} className="note note-warn" role="status" style={{ marginBottom: 12 }}>
          <b style={{ color: 'var(--warn)' }}>⚠ 注意</b>
          <span style={{ flex: 1 }}>{w}</span>
        </div>
      ))}
      <div style={{ fontSize: 13, color: 'var(--t2)', marginBottom: 8 }}>
        把下面整段合并进 <span className="mono">~/.claude/settings.json</span> 的顶层(已有 <span className="mono">env</span> 则合并其键值),然后重启 Claude Code。
      </div>
      <div style={{ position: 'relative' }}>
        <pre className="code">
          {isLoading ? '生成中…' : (data?.settingsJson ?? '')}
        </pre>
        <Btn
          size="sm"
          style={{ position: 'absolute', top: 8, right: 8 }}
          disabled={!data}
          onClick={() => data && copy(data.settingsJson)}
        >
          复制
        </Btn>
      </div>
    </Modal>
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

  return (
    <Modal
      title={token ? `自检 · ${token.name}` : '令牌自检'}
      open={!!token}
      onClose={onClose}
      width={560}
      footer={<Btn variant="primary" onClick={onClose}>关闭</Btn>}
    >
      <div className="note note-warn" role="status" style={{ marginBottom: 14 }}>
        <span>选择模型后核查「状态 / 有效期 / 授权 / 可用性 / 额度 / 余额」,<b>不访问上游、不计费、不消耗限速</b>。</span>
      </div>
      <div style={{ marginBottom: 14 }}>
        <Select
          value={model || undefined}
          onChange={e => setModel(e.target.value)}
          style={{ width: '100%' }}
        >
          <option value="">选择要自检的模型</option>
          {models.map(m => <option key={m.name} value={m.name}>{m.name}</option>)}
        </Select>
      </div>
      {!model ? (
        <div style={{ fontSize: 13, color: 'var(--t3)' }}>请先选择一个模型。</div>
      ) : isFetching ? (
        <SkBlock />
      ) : isError ? (
        <ErrorState
          title="自检失败"
          desc={(error as Error)?.message || '无法完成自检,请稍后重试。'}
          onRetry={() => void refetch()}
        />
      ) : data ? (
        <div className="table-wrap">
          <table className="tb">
            <thead>
              <tr>
                <th style={{ width: 110 }}>检查项</th>
                <th style={{ width: 90 }}>结果</th>
                <th>说明</th>
              </tr>
            </thead>
            <tbody>
              {data.checks.map(c => (
                <tr key={c.name}>
                  <td><span style={{ color: 'var(--t2)' }}>{c.name}</span></td>
                  <td><StatusDot tone={c.ok ? 'ok' : 'err'} text={c.ok ? '通过' : '未通过'} /></td>
                  <td><span style={{ color: 'var(--t3)' }}>{c.detail || '—'}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      {data && (
        <div style={{ marginTop: 12, fontSize: 13 }}>
          {data.ok
            ? <span style={{ color: 'var(--ok)' }}>✓ 该令牌可用「{data.model}」发起调用。</span>
            : <span style={{ color: 'var(--err)' }}>✗ 该令牌当前不能使用「{data.model}」,请按上表逐项处理。</span>}
        </div>
      )}
    </Modal>
  );
}

export default function Tokens() {
  const qc = useQueryClient();
  const isAdmin = useSession(s => s.admin?.role) === 'admin';
  const [editor, setEditor] = useState<{ open: boolean; initial: GatewayToken | null }>({ open: false, initial: null });
  const [created, setCreated] = useState<TokenCreateResult | null>(null);
  const [configToken, setConfigToken] = useState<GatewayToken | null>(null);
  const [probeToken, setProbeToken] = useState<GatewayToken | null>(null);
  const [ownerFilter, setOwnerFilter] = useState<number | 'all'>('all');

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
      : tokens.filter(t => (ownerFilter === 0 ? t.ownerId == null : t.ownerId === ownerFilter))),
    [tokens, isAdmin, ownerFilter],
  );

  const refresh = () => qc.invalidateQueries({ queryKey: ['tokens'] });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteToken(id),
    onSuccess: () => { toast.ok('已删除'); refresh(); },
    onError: e => toast.err(`删除失败:${errMsg(e)}`),
  });

  const saveToken = async (draft: TokenDraft, id?: number) => {
    try {
      if (id != null) {
        await api.updateToken(id, draft);
        toast.ok('令牌已保存');
      } else {
        const r = await api.createToken(draft);
        setCreated(r);
        toast.ok('令牌已创建');
      }
      refresh();
      setEditor({ open: false, initial: null });
    } catch (e) {
      toast.err(`保存失败:${errMsg(e)}`);
    }
  };

  const confirmDelete = (t: GatewayToken) => {
    confirm({
      title: `删除令牌「${t.name}」?`,
      body: '删除后该 Key 立即失效；历史日志中的令牌名称仍会保留。',
      okText: '删除',
      cancelText: '取消',
      danger: true,
    }).then(ok => { if (ok) remove.mutate(t.id); });
  };

  /** 额度条：颜色随占比升级，但文字始终给出具体金额与百分比。 */
  const quotaCell = (r: GatewayToken) => {
    if (r.quotaUsd <= 0) {
      return (
        <span className="num" style={{ fontSize: 13, color: 'var(--t3)' }}>
          {fmt.usd(r.usedUsd)} / 不限
        </span>
      );
    }
    const rate = Math.min(r.usedUsd / r.quotaUsd, 1);
    const tone = rate >= QUOTA_ALERT ? 'warn' : 'primary';
    return (
      <div>
        <div style={{ height: 6, background: 'var(--panel-3)', border: '1px solid var(--line)', borderRadius: 2, overflow: 'hidden' }}>
          <i style={{ display: 'block', height: '100%', width: `${rate * 100}%`, background: tone === 'warn' ? 'var(--warn)' : 'var(--brand)' }} />
        </div>
        <div className="num" style={{ fontSize: 12.5, color: 'var(--t3)', marginTop: 5 }}>
          {fmt.usd(r.usedUsd)} / {fmt.usd(r.quotaUsd)} · {(rate * 100).toFixed(0)}%
        </div>
      </div>
    );
  };

  const emptyNode = isError ? (
    <ErrorState title="令牌列表加载失败" desc="无法读取令牌，对外分发的 Key 不受影响。" onRetry={() => void refetch()} />
  ) : tokens.length === 0 ? (
    <EmptyState
      title="还没有访问令牌"
      desc="令牌是对外分发的网关 Key。创建后可用它调用网关，并单独限制额度与速率。"
      action={<Btn size="sm" variant="primary" onClick={() => setEditor({ open: true, initial: null })}>新建令牌</Btn>}
    />
  ) : (
    <NoResultState title="没有符合归属筛选的令牌" desc="换一个归属用户，或切回「全部归属」。" />
  );

  return (
    <div>
      <PageHeader
        title="访问令牌"
        desc="对外分发的网关 Key，可独立设额度、限流与过期时间"
        extra={
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            {isAdmin && (
              <Select
                value={ownerFilter}
                onChange={e => setOwnerFilter(e.target.value === 'all' ? 'all' : Number(e.target.value))}
                style={{ width: 180 }}
              >
                <option value="all">全部归属</option>
                <option value={0}>全局(管理员)</option>
                {users.map(u => <option key={u.id} value={u.id}>{u.username}</option>)}
              </Select>
            )}
            <Btn variant="primary" onClick={() => setEditor({ open: true, initial: null })}>新建令牌</Btn>
          </div>
        }
      />

      <Blocks>
        {!isAdmin && wallet && (
          <div
            style={{
              display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14,
              fontSize: 13.5, color: 'var(--t2)',
            }}
          >
            <span style={{ color: 'var(--t3)' }}>账户余额</span>
            <b className="num" style={{ color: wallet.balanceUsd <= 0 ? 'var(--err)' : 'var(--t1)' }}>
              {fmt.usd(wallet.balanceUsd)}
            </b>
            <span style={{ color: 'var(--t3)' }}>
              {wallet.balanceUsd <= 0
                ? '余额不足，调用已被拒绝，请联系管理员充值'
                : '每次调用按本站售价从余额扣除；令牌额度为单令牌上限'}
            </span>
          </div>
        )}
        <BlockCard>
          {isLoading && tokens.length === 0 ? (
            <div style={{ padding: 20 }}><SkBlock /></div>
          ) : view.length === 0 ? (
            emptyNode
          ) : (
            <div className="table-wrap">
              <table className="tb" style={{ tableLayout: 'fixed' }}>
                <colgroup>
                  <col style={{ width: 240 }} />
                  <col style={{ width: 140 }} />
                  <col style={{ width: 158 }} />
                  <col style={{ width: 72 }} />
                  <col style={{ width: 108 }} />
                  <col style={{ width: 120 }} />
                  <col style={{ width: 78 }} />
                  <col style={{ width: 172 }} />
                </colgroup>
                <thead>
                  <tr>
                    <th>令牌</th>
                    <th>可用模型</th>
                    <th>额度使用</th>
                    <th className="right">RPM</th>
                    <th>过期时间</th>
                    <th>最后使用</th>
                    <th>状态</th>
                    <th className="right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {view.map(r => (
                    <tr key={r.id}>
                      <td>
                        <div style={{ minWidth: 0, overflow: 'hidden' }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
                            <b
                              title={r.name}
                              style={{ fontWeight: 500, color: 'var(--t1)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: '1 1 auto', minWidth: 0 }}
                            >{r.name}</b>
                            {isAdmin && (r.ownerId == null
                              ? <span style={{ color: 'var(--t3)', fontSize: 12.5, flexShrink: 0 }}>全局</span>
                              : <Badge tone="slate">{r.ownerName}</Badge>)}
                          </div>
                          <div className="mono" style={{ fontSize: 12, color: 'var(--t3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                            {r.keyMasked}
                          </div>
                        </div>
                      </td>
                      <td>
                        {(() => {
                          const list = r.allowedModels as string[];
                          if (list.length === 1 && list[0] === '*') return <Badge tone="slate">不限</Badge>;
                          if (!list.length) return <span style={{ color: 'var(--t3)' }}>无</span>;
                          const text = list.join('、');
                          return <span className="mono" title={text}>{text}</span>;
                        })()}
                      </td>
                      <td>{quotaCell(r)}</td>
                      <td className="right num">{r.rpmLimit || '不限'}</td>
                      <td>
                        {!r.expiresAt ? <span style={{ color: 'var(--t3)' }}>永不过期</span>
                          : (() => { const d = dayjs(r.expiresAt); return d.isValid() ? <span className="num">{d.format('YYYY-MM-DD')}</span> : <span className="mono">{r.expiresAt}</span>; })()}
                      </td>
                      <td>
                        {!r.lastUsedAt ? <span style={{ color: 'var(--t3)' }}>从未使用</span>
                          : (() => { const d = dayjs(r.lastUsedAt); return <span className="num" style={{ color: 'var(--t3)' }}>{d.isValid() ? d.format('YYYY-MM-DD HH:mm') : r.lastUsedAt}</span>; })()}
                      </td>
                      <td><StatusDot status={r.status} /></td>
                      <td className="right">
                        <span className="taglist" style={{ justifyContent: 'flex-end' }}>
                          <Btn
                            size="sm"
                            disabled={!r.keyRetrievable}
                            title={r.keyRetrievable ? undefined : '旧密钥无法回显，请重新创建'}
                            onClick={() => setConfigToken(r)}
                          >生成配置</Btn>
                          <Btn size="sm" variant="ghost" onClick={() => setEditor({ open: true, initial: r })}>编辑</Btn>
                          <Dropdown
                            menu={[
                              { key: 'probe', label: '自检', onClick: () => setProbeToken(r) },
                              { key: 'delete', label: '删除', danger: true, onClick: () => confirmDelete(r) },
                            ]}
                          >
                            {() => <IconBtn n="more" title={`更多操作 ${r.name}`} />}
                          </Dropdown>
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

      {created && (
        <Modal
          open={!!created}
          title="令牌已创建"
          onClose={() => setCreated(null)}
          footer={<Btn variant="primary" onClick={() => setCreated(null)}>我已妥善保存</Btn>}
        >
          <div className="note note-warn" role="status" style={{ marginBottom: 16 }}>
            <b>密钥已加密存储</b>
            <span>之后可在列表用「生成配置」再次获取完整 Key。</span>
          </div>
          {created && (
            <>
              <div style={{ fontSize: 13, color: 'var(--t2)', marginBottom: 14 }}>
                掩码形式
                <span className="mono" style={{ marginLeft: 8, color: 'var(--t3)' }}>{created.token.keyMasked}</span>
              </div>
              <div style={{ fontSize: 13, color: 'var(--t2)', marginBottom: 6 }}>完整 Key</div>
              <div
                style={{
                  display: 'flex', alignItems: 'center', gap: 8, wordBreak: 'break-all',
                  fontSize: 13,
                  border: '1px solid var(--line)', borderRadius: 4, padding: 12,
                  background: 'var(--bg)',
                }}
              >
                <Copyable text={created.key}><span className="mono">{created.key}</span></Copyable>
              </div>
            </>
          )}
        </Modal>
      )}

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
    </div>
  );
}
