import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { Block as BlockCard, Blocks } from '@/components/Block';
import LedgerDrawer from '@/components/LedgerDrawer';
import { EmptyState, ErrorState } from '@/components/States';
import { Badge, Btn, Field, Input, PageHead, Select } from '@/ui/primitives';
import { Dropdown, Modal, confirm } from '@/ui/overlay';
import { toast } from '@/ui/toast';
import { Icon } from '@/ui/icons';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { fmt } from '@/utils/format';
import type { Role, UserAccount } from '@/types';

const errMsg = (e: unknown) => (e instanceof Error ? e.message : '请稍后重试');

/** 角色徽章：文字 + 语义色，不靠颜色单独表意。 */
function RoleBadge({ role }: { role: Role }) {
  const admin = role === 'admin';
  return <Badge tone={admin ? 'brand-o' : 'slate'}>{admin ? '管理员' : '普通用户'}</Badge>;
}

/** 数值输入统一按字符串承接,空串 = 未填(校验时判必填)。 */
const numOrNull = (v: string) => (v === '' ? null : Number(v));

/** 新建用户弹窗(角色默认普通用户)。 */
function CreateUserModal(props: {
  open: boolean;
  onCancel: () => void;
  onSubmit: (v: { username: string; password: string; role: Role }) => Promise<void>;
}) {
  const { open, onCancel, onSubmit } = props;
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<Role>('user');
  const [errs, setErrs] = useState<{ username?: string; password?: string }>({});
  const [saving, setSaving] = useState(false);

  // destroyOnHidden 等价:每次打开都是干净表单
  useEffect(() => {
    if (open) { setUsername(''); setPassword(''); setRole('user'); setErrs({}); }
  }, [open]);

  const submit = async () => {
    // 校验规则逐条照搬原表单 rules(required + whitespace / min 8)
    const e: typeof errs = {};
    if (!username.trim()) e.username = '请输入用户名';
    if (password.length < 8) e.password = '至少 8 位';
    setErrs(e);
    if (Object.keys(e).length > 0) return;
    setSaving(true);
    try {
      await onSubmit({ username: username.trim(), password, role });
      setUsername(''); setPassword('');
    } catch (e) {
      toast.err(`创建失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={open} onClose={onCancel} title="新建用户" width={440}
      footer={
        <>
          <Btn onClick={onCancel}>取消</Btn>
          <Btn variant="primary" loading={saving} onClick={submit}>创建</Btn>
        </>
      }
    >
      <Field label="用户名" err={errs.username}>
        <Input placeholder="例如:alice" maxLength={64} value={username} err={!!errs.username}
          onChange={e => setUsername(e.target.value)} />
      </Field>
      <Field label="初始密码" err={errs.password}>
        <Input type="password" autoComplete="new-password" placeholder="至少 8 位,告知用户后由其自行修改"
          value={password} err={!!errs.password} onChange={e => setPassword(e.target.value)} />
      </Field>
      <Field label="角色">
        <Select value={role} onChange={e => setRole(e.target.value as Role)}>
          <option value="user">普通用户(仅能管理自己的访问令牌)</option>
          <option value="admin">管理员(全部权限)</option>
        </Select>
      </Field>
    </Modal>
  );
}

/** 重置他人密码弹窗。 */
function ResetPasswordModal(props: {
  user: UserAccount | null;
  onCancel: () => void;
  onSubmit: (id: number, newPassword: string) => Promise<void>;
}) {
  const { user, onCancel, onSubmit } = props;
  const [newPassword, setNewPassword] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => { if (user) { setNewPassword(''); setErr(null); } }, [user]);

  const submit = async () => {
    if (!user) return;
    if (newPassword.length < 8) { setErr('至少 8 位'); return; }
    setErr(null);
    setSaving(true);
    try {
      await onSubmit(user.id, newPassword);
      setNewPassword('');
    } catch (e) {
      toast.err(`重置失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={!!user} onClose={onCancel} title={user ? `重置「${user.username}」的密码` : '重置密码'} width={440}
      footer={
        <>
          <Btn onClick={onCancel}>取消</Btn>
          <Btn variant="primary" loading={saving} onClick={submit}>重置</Btn>
        </>
      }
    >
      <Field label="新密码" err={err ?? undefined}>
        <Input type="password" autoComplete="new-password" value={newPassword} err={!!err}
          onChange={e => setNewPassword(e.target.value)} />
      </Field>
    </Modal>
  );
}

/** 充值/扣减弹窗(正数充值,负数扣减调整)。 */
function TopupModal(props: {
  user: UserAccount | null;
  onCancel: () => void;
  onSubmit: (id: number, amount: number, note?: string) => Promise<void>;
}) {
  const { user, onCancel, onSubmit } = props;
  const [amount, setAmount] = useState('100');
  const [note, setNote] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => { if (user) { setAmount('100'); setNote(''); setErr(null); } }, [user]);

  const submit = async () => {
    if (!user) return;
    const n = numOrNull(amount);
    if (n === null || !Number.isFinite(n)) { setErr('请输入金额'); return; }
    setErr(null);
    setSaving(true);
    try {
      await onSubmit(user.id, n, note.trim() || undefined);
      setNote('');
    } catch (e) {
      toast.err(`充值失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={!!user} onClose={onCancel} title={user ? `调整「${user.username}」的余额` : '调整余额'} width={440}
      footer={
        <>
          <Btn onClick={onCancel}>取消</Btn>
          <Btn variant="primary" loading={saving} onClick={submit}>提交</Btn>
        </>
      }
    >
      {user && (
        <div style={{ marginBottom: 16, fontSize: 13, color: 'var(--t3)' }}>
          当前余额:<b className="num" style={{ color: 'var(--t1)' }}>{fmt.usd(user.balanceUsd)}</b>
        </div>
      )}
      <Field label="金额(正=充值,负=扣减)" err={err ?? undefined}>
        <Input type="number" step={100} placeholder="例如 100 或 -50" value={amount} err={!!err}
          onChange={e => setAmount(e.target.value)} />
      </Field>
      <Field label="备注(可选)">
        <Input placeholder="例如:微信转账 / 试用额度" maxLength={120} value={note}
          onChange={e => setNote(e.target.value)} />
      </Field>
    </Modal>
  );
}

/** 令牌上限弹窗:限制该客户自建令牌能授权的额度/RPM 上限(0 = 不限)。 */
function CeilingModal(props: {
  user: UserAccount | null;
  onCancel: () => void;
  onSubmit: (id: number, quotaUsd: number, rpmLimit: number) => Promise<void>;
}) {
  const { user, onCancel, onSubmit } = props;
  const [quotaUsd, setQuotaUsd] = useState('0');
  const [rpmLimit, setRpmLimit] = useState('0');
  const [saving, setSaving] = useState(false);

  useEffect(() => { if (user) { setQuotaUsd('0'); setRpmLimit('0'); } }, [user]);

  const submit = async () => {
    if (!user) return;
    setSaving(true);
    try {
      await onSubmit(user.id, numOrNull(quotaUsd) ?? 0, numOrNull(rpmLimit) ?? 0);
    } catch (e) {
      toast.err(`保存失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={!!user} onClose={onCancel} title={user ? `设置「${user.username}」的令牌上限` : '令牌上限'} width={440}
      footer={
        <>
          <Btn onClick={onCancel}>取消</Btn>
          <Btn variant="primary" loading={saving} onClick={submit}>保存</Btn>
        </>
      }
    >
      <div style={{ marginBottom: 16, fontSize: 13, color: 'var(--t3)' }}>
        该客户自助创建的令牌不得超过此上限(0 = 不限)。真正常住闸门是余额,这里是令牌子预算的天花板。
      </div>
      <Field label="令牌额度上限" hint="单位同计价币种;0 = 不限">
        <Input type="number" min={0} step={10} placeholder="0 = 不限" value={quotaUsd}
          onChange={e => setQuotaUsd(e.target.value)} />
      </Field>
      <Field label="令牌 RPM 上限" hint="0 = 不限">
        <Input type="number" min={0} max={100000} placeholder="0 = 不限" value={rpmLimit}
          onChange={e => setRpmLimit(e.target.value)} />
      </Field>
    </Modal>
  );
}

export default function Users() {
  const qc = useQueryClient();
  const me = useSession(s => s.admin);
  const [creating, setCreating] = useState(false);
  const [resetting, setResetting] = useState<UserAccount | null>(null);
  const [topupUser, setTopupUser] = useState<UserAccount | null>(null);
  const [ceilingUser, setCeilingUser] = useState<UserAccount | null>(null);
  const [ledgerUser, setLedgerUser] = useState<UserAccount | null>(null);

  const { data: users = [], isLoading, isError, refetch } = useQuery({ queryKey: ['users'], queryFn: api.getUsers });
  const refresh = () => qc.invalidateQueries({ queryKey: ['users'] });

  // 账变流水(管理员审计):打开抽屉时才拉取
  const { data: ledgerLogs = [] } = useQuery({
    queryKey: ['user-balance-logs', ledgerUser?.id],
    queryFn: () => api.userBalanceLogs(ledgerUser!.id),
    enabled: !!ledgerUser,
  });

  const adminCount = users.filter(u => u.role === 'admin').length;

  const create = async (v: { username: string; password: string; role: Role }) => {
    await api.createUser(v);
    toast.ok('用户已创建');
    setCreating(false);
    refresh();
  };

  const resetPw = async (id: number, newPassword: string) => {
    await api.resetUserPassword(id, newPassword);
    toast.ok('密码已重置');
    setResetting(null);
  };

  const topup = async (id: number, amount: number, note?: string) => {
    await api.topupUser(id, amount, note);
    toast.ok(amount >= 0 ? '已充值' : '已扣减');
    setTopupUser(null);
    refresh();
  };

  const saveCeiling = async (id: number, quotaUsd: number, rpmLimit: number) => {
    await api.setUserCeiling(id, quotaUsd, rpmLimit);
    toast.ok('令牌上限已保存');
    setCeilingUser(null);
    refresh();
  };

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteUser(id),
    onSuccess: () => { toast.ok('已删除'); refresh(); },
    onError: e => toast.err(`删除失败:${errMsg(e)}`),
  });

  const confirmDelete = (u: UserAccount) => {
    confirm({
      title: `删除用户「${u.username}」?`,
      body: `该用户名下 ${u.keyCount} 个访问令牌将一并删除且立即失效;历史日志中的令牌名称保留。`,
      okText: '删除',
      danger: true,
      cancelText: '取消',
    }).then(ok => { if (ok) remove.mutate(u.id); });
  };

  return (
    <div>
      <PageHead
        title="用户管理"
        sub="账号与角色；普通用户只能管理自己的访问令牌"
        actions={<Btn variant="primary" onClick={() => setCreating(true)}>新建用户</Btn>}
      />

      <Blocks>
        <BlockCard>
          {isError ? (
            <div style={{ padding: '18px 20px' }}>
              <ErrorState
                title="用户列表加载失败"
                desc="无法读取账号列表，登录会话仍然有效。"
                onRetry={() => void refetch()}
              />
            </div>
          ) : (
            <div className="table-wrap" style={{ padding: '8px 6px 0' }}>
              <table className="tb" style={{ tableLayout: 'fixed' }}>
                <thead>
                  <tr>
                    <th>用户名</th>
                    <th style={{ width: 96 }}>角色</th>
                    <th className="right" style={{ width: 116 }}>余额</th>
                    <th className="right" style={{ width: 110 }}>令牌上限</th>
                    <th className="right" style={{ width: 90 }}>Key 数量</th>
                    <th style={{ width: 150 }}>创建时间</th>
                    {/* 高频（充值）外露，低频（账变/令牌上限/重置密码/删除）收进「更多」 */}
                    <th className="right" style={{ width: 120 }}>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {isLoading && users.length === 0 ? (
                    <tr><td colSpan={7} style={{ textAlign: 'center', color: 'var(--t3)', padding: 24 }}>读取中…</td></tr>
                  ) : users.length === 0 ? (
                    <tr>
                      <td colSpan={7}>
                        <EmptyState
                          title="还没有其他账号"
                          desc="这里只有你自己。新建普通用户后，他们各自管理自己的访问令牌。"
                          action={<Btn size="sm" variant="primary" onClick={() => setCreating(true)}>新建用户</Btn>}
                        />
                      </td>
                    </tr>
                  ) : (
                    users.map(r => {
                      const isSelf = r.id === me?.id;
                      const isLastAdmin = r.role === 'admin' && adminCount <= 1;
                      const isUser = r.role === 'user';
                      // Dropdown 菜单项无 disabled 语义:不可用项压灰并在 onClick 里拦截
                      const off = { color: 'var(--t3)', opacity: .55 } as const;
                      return (
                        <tr key={r.id}>
                          <td><b style={{ fontWeight: 500, color: 'var(--t1)' }}>{r.username}</b></td>
                          <td><RoleBadge role={r.role} /></td>
                          <td className="right">
                            {r.role !== 'user' ? (
                              <span style={{ color: 'var(--t3)' }}>—</span>
                            ) : (
                              <span className="num" style={{ color: r.balanceUsd <= 0 ? 'var(--err)' : undefined }}>{fmt.usd(r.balanceUsd)}</span>
                            )}
                          </td>
                          <td className="right">
                            {r.role !== 'user' ? (
                              <span style={{ color: 'var(--t3)' }}>—</span>
                            ) : r.tokenQuotaCeiling <= 0 && r.tokenRpmCeiling <= 0 ? (
                              <span style={{ color: 'var(--t3)' }}>不限</span>
                            ) : (
                              <span className="num" title={`RPM 上限 ${r.tokenRpmCeiling > 0 ? r.tokenRpmCeiling : '不限'}`}>
                                {r.tokenQuotaCeiling > 0 ? fmt.usd(r.tokenQuotaCeiling) : 'RPM 限'}
                              </span>
                            )}
                          </td>
                          <td className="right num">{r.keyCount}</td>
                          <td>
                            {(() => {
                              const d = dayjs(r.createdAt);
                              return <span className="num" style={{ color: 'var(--t3)' }}>{d.isValid() ? d.format('YYYY-MM-DD HH:mm') : r.createdAt}</span>;
                            })()}
                          </td>
                          <td className="right">
                            <span className="taglist" style={{ justifyContent: 'flex-end' }}>
                              <Btn size="sm" disabled={!isUser} title={isUser ? undefined : '管理员无钱包'}
                                onClick={() => setTopupUser(r)}>
                                充值
                              </Btn>
                              <Dropdown
                                menu={[
                                  { key: 'ledger', label: '账变流水', onClick: () => setLedgerUser(r) },
                                  { key: 'ceiling', label: <span style={!isUser ? off : undefined}>令牌上限</span>, onClick: () => { if (isUser) setCeilingUser(r); } },
                                  { key: 'password', label: <span style={isSelf ? off : undefined}>重置密码</span>, onClick: () => { if (!isSelf) setResetting(r); } },
                                  { key: 'delete', label: <span style={isSelf || isLastAdmin ? off : undefined}>删除</span>, danger: true, onClick: () => { if (!isSelf && !isLastAdmin) confirmDelete(r); } },
                                ]}
                              >
                                {() => (
                                  <button
                                    type="button"
                                    className="icon-btn"
                                    aria-label={`更多操作 ${r.username}`}
                                    title={isLastAdmin ? '最后一个管理员不可删除' : undefined}
                                  >
                                    <Icon n="more" />
                                  </button>
                                )}
                              </Dropdown>
                            </span>
                          </td>
                        </tr>
                      );
                    })
                  )}
                </tbody>
              </table>
            </div>
          )}
        </BlockCard>
      </Blocks>

      <CreateUserModal open={creating} onCancel={() => setCreating(false)} onSubmit={create} />
      <ResetPasswordModal user={resetting} onCancel={() => setResetting(null)} onSubmit={resetPw} />
      <TopupModal user={topupUser} onCancel={() => setTopupUser(null)} onSubmit={topup} />
      <CeilingModal user={ceilingUser} onCancel={() => setCeilingUser(null)} onSubmit={saveCeiling} />
      <LedgerDrawer open={!!ledgerUser} logs={ledgerLogs} onClose={() => setLedgerUser(null)} />
    </div>
  );
}
