import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import { EmptyState, ErrorState } from '@/components/States';
import { Badge, Button, Field, HStack, Input, Select, SkLines, Table } from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { Icon } from '@/ui/icons';
import { Dropdown, confirm } from '@/ui/overlay';
import type { MenuItem } from '@/ui/overlay';
import { message } from '@/ui/toast';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { fmt } from '@/utils/format';
import type { Role, UserAccount } from '@/types';

const errMsg = (e: unknown) => (e instanceof Error ? e.message : '请稍后重试');

/** 角色徽章：文字 + 语义色，不靠颜色单独表意。 */
function RoleBadge({ role }: { role: Role }) {
  return role === 'admin' ? <Badge tone="brand">管理员</Badge> : <Badge tone="slate">普通用户</Badge>;
}

/** 弹窗外壳：自绘 `.overlay` + `.modal`，含标题栏与底部操作。 */
function ModalShell({
  title,
  onClose,
  foot,
  children,
}: {
  title: string;
  onClose: () => void;
  foot: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div
      className="overlay"
      style={{ zIndex: 70 }}
      onMouseDown={e => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal" style={{ maxWidth: 440 }}>
        <div className="modal-head">
          <div style={{ flex: 1, minWidth: 0 }}>
            <h3>{title}</h3>
          </div>
          <button className="icon-btn" onClick={onClose} aria-label="关闭">
            <Icon name="x" size={13} />
          </button>
        </div>
        <div className="modal-body">{children}</div>
        <div className="modal-foot">{foot}</div>
      </div>
    </div>
  );
}

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
  const [errors, setErrors] = useState<{ username?: string; password?: string }>({});
  const [saving, setSaving] = useState(false);

  if (!open) return null;

  const submit = async () => {
    const e: typeof errors = {};
    if (!username.trim()) e.username = '请输入用户名';
    if (!password) e.password = '请输入初始密码';
    else if (password.length < 8) e.password = '至少 8 位';
    setErrors(e);
    if (e.username || e.password) return;

    setSaving(true);
    try {
      await onSubmit({ username: username.trim(), password, role });
      setUsername('');
      setPassword('');
      setRole('user');
      setErrors({});
    } catch (ex) {
      message.error(`创建失败:${errMsg(ex)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <ModalShell
      title="新建用户"
      onClose={onCancel}
      foot={
        <>
          <Button variant="secondary" onClick={onCancel}>取消</Button>
          <Button variant="primary" loading={saving} onClick={() => void submit()}>创建</Button>
        </>
      }
    >
      <div className="stack">
        <Field label="用户名" error={errors.username}>
          <Input
            value={username}
            invalid={!!errors.username}
            maxLength={64}
            placeholder="例如:alice"
            onChange={e => {
              setUsername(e.target.value);
              if (errors.username) setErrors(s => ({ ...s, username: undefined }));
            }}
          />
        </Field>
        <Field label="初始密码" error={errors.password} hint={errors.password ? undefined : '至少 8 位,告知用户后由其自行修改'}>
          <Input
            type="password"
            value={password}
            invalid={!!errors.password}
            autoComplete="new-password"
            onChange={e => {
              setPassword(e.target.value);
              if (errors.password) setErrors(s => ({ ...s, password: undefined }));
            }}
          />
        </Field>
        <Field label="角色">
          <Select
            value={role}
            onChange={e => setRole(e.target.value as Role)}
            options={[
              { value: 'user', label: '普通用户(仅能管理自己的访问令牌)' },
              { value: 'admin', label: '管理员(全部权限)' },
            ]}
          />
        </Field>
      </div>
    </ModalShell>
  );
}

/** 重置他人密码弹窗。 */
function ResetPasswordModal(props: {
  user: UserAccount | null;
  onCancel: () => void;
  onSubmit: (id: number, newPassword: string) => Promise<void>;
}) {
  const { user, onCancel, onSubmit } = props;
  const [pw, setPw] = useState('');
  const [error, setError] = useState<string>();
  const [saving, setSaving] = useState(false);

  if (!user) return null;

  const submit = async () => {
    if (!pw) return setError('请输入新密码');
    if (pw.length < 8) return setError('至少 8 位');
    setSaving(true);
    try {
      await onSubmit(user.id, pw);
      setPw('');
    } catch (e) {
      message.error(`重置失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <ModalShell
      title={`重置「${user.username}」的密码`}
      onClose={onCancel}
      foot={
        <>
          <Button variant="secondary" onClick={onCancel}>取消</Button>
          <Button variant="primary" loading={saving} onClick={() => void submit()}>重置</Button>
        </>
      }
    >
      <Field label="新密码" error={error} hint={error ? undefined : '至少 8 位'}>
        <Input
          type="password"
          value={pw}
          invalid={!!error}
          autoComplete="new-password"
          onChange={e => {
            setPw(e.target.value);
            if (error) setError(undefined);
          }}
        />
      </Field>
    </ModalShell>
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
  const [error, setError] = useState<string>();
  const [saving, setSaving] = useState(false);

  if (!user) return null;

  const submit = async () => {
    const v = Number(amount);
    if (amount.trim() === '' || !Number.isFinite(v)) return setError('请输入金额');
    setSaving(true);
    try {
      await onSubmit(user.id, v, note.trim() || undefined);
      setAmount('100');
      setNote('');
    } catch (e) {
      message.error(`充值失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <ModalShell
      title={`调整「${user.username}」的余额`}
      onClose={onCancel}
      foot={
        <>
          <Button variant="secondary" onClick={onCancel}>取消</Button>
          <Button variant="primary" loading={saving} onClick={() => void submit()}>提交</Button>
        </>
      }
    >
      <div style={{ marginBottom: 16, fontSize: 13, color: 'var(--t3)' }}>
        当前余额:<b className="num" style={{ color: 'var(--t1)' }}>{fmt.usd(user.balanceUsd)}</b>
      </div>
      <div className="stack">
        <Field label="金额(正=充值,负=扣减)" error={error}>
          <Input
            type="number"
            step={100}
            value={amount}
            invalid={!!error}
            placeholder="例如 100 或 -50"
            onChange={e => {
              setAmount(e.target.value);
              if (error) setError(undefined);
            }}
          />
        </Field>
        <Field label="备注(可选)">
          <Input value={note} maxLength={120} placeholder="例如:微信转账 / 试用额度" onChange={e => setNote(e.target.value)} />
        </Field>
      </div>
    </ModalShell>
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

  if (!user) return null;

  const submit = async () => {
    const q = Number(quotaUsd);
    const r = Number(rpmLimit);
    setSaving(true);
    try {
      await onSubmit(user.id, Number.isFinite(q) ? q : 0, Number.isFinite(r) ? r : 0);
      setQuotaUsd('0');
      setRpmLimit('0');
    } catch (e) {
      message.error(`保存失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <ModalShell
      title={`设置「${user.username}」的令牌上限`}
      onClose={onCancel}
      foot={
        <>
          <Button variant="secondary" onClick={onCancel}>取消</Button>
          <Button variant="primary" loading={saving} onClick={() => void submit()}>保存</Button>
        </>
      }
    >
      <div style={{ marginBottom: 16, fontSize: 13, color: 'var(--t3)' }}>
        该客户自助创建的令牌不得超过此上限(0 = 不限)。真正常住闸门是余额,这里是令牌子预算的天花板。
      </div>
      <div className="stack">
        <Field label="令牌额度上限" hint="单位同计价币种;0 = 不限">
          <Input type="number" min={0} step={10} value={quotaUsd} placeholder="0 = 不限" onChange={e => setQuotaUsd(e.target.value)} />
        </Field>
        <Field label="令牌 RPM 上限" hint="0 = 不限">
          <Input type="number" min={0} max={100000} value={rpmLimit} placeholder="0 = 不限" onChange={e => setRpmLimit(e.target.value)} />
        </Field>
      </div>
    </ModalShell>
  );
}

export default function Users() {
  const qc = useQueryClient();
  const me = useSession(s => s.admin);
  const [creating, setCreating] = useState(false);
  const [resetting, setResetting] = useState<UserAccount | null>(null);
  const [topupUser, setTopupUser] = useState<UserAccount | null>(null);
  const [ceilingUser, setCeilingUser] = useState<UserAccount | null>(null);
  const { data: users = [], isLoading, isError, refetch } = useQuery({ queryKey: ['users'], queryFn: api.getUsers });
  const refresh = () => qc.invalidateQueries({ queryKey: ['users'] });

  const adminCount = users.filter(u => u.role === 'admin').length;

  const create = async (v: { username: string; password: string; role: Role }) => {
    await api.createUser(v);
    message.success('用户已创建');
    setCreating(false);
    refresh();
  };

  const resetPw = async (id: number, newPassword: string) => {
    await api.resetUserPassword(id, newPassword);
    message.success('密码已重置');
    setResetting(null);
  };

  const topup = async (id: number, amount: number, note?: string) => {
    await api.topupUser(id, amount, note);
    message.success(amount >= 0 ? '已充值' : '已扣减');
    setTopupUser(null);
    refresh();
  };

  const saveCeiling = async (id: number, quotaUsd: number, rpmLimit: number) => {
    await api.setUserCeiling(id, quotaUsd, rpmLimit);
    message.success('令牌上限已保存');
    setCeilingUser(null);
    refresh();
  };

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteUser(id),
    onSuccess: () => { message.success('已删除'); refresh(); },
    onError: e => message.error(`删除失败:${errMsg(e)}`),
  });

  const confirmDelete = async (u: UserAccount) => {
    const ok = await confirm({
      title: `删除用户「${u.username}」?`,
      content: `该用户名下 ${u.keyCount} 个访问令牌将一并删除且立即失效;历史日志中的令牌名称保留。`,
      okText: '删除',
      danger: true,
    });
    if (ok) remove.mutate(u.id);
  };

  const columns: Column<UserAccount>[] = [
    { key: 'username', title: '用户名', render: r => <span className="strong">{r.username}</span> },
    { key: 'role', title: '角色', width: 96, render: r => <RoleBadge role={r.role} /> },
    {
      key: 'balanceUsd',
      title: '余额',
      width: 116,
      align: 'right',
      render: r =>
        r.role !== 'user' ? (
          <span className="muted">—</span>
        ) : (
          <span className="num" style={{ color: r.balanceUsd <= 0 ? 'var(--err)' : undefined }}>{fmt.usd(r.balanceUsd)}</span>
        ),
    },
    {
      key: 'ceiling',
      title: '令牌上限',
      width: 110,
      align: 'right',
      render: r => {
        if (r.role !== 'user') return <span className="muted">—</span>;
        if (r.tokenQuotaCeiling <= 0 && r.tokenRpmCeiling <= 0) {
          return <span className="muted">不限</span>;
        }
        return (
          <span className="num" title={`RPM 上限 ${r.tokenRpmCeiling > 0 ? r.tokenRpmCeiling : '不限'}`}>
            {r.tokenQuotaCeiling > 0 ? fmt.usd(r.tokenQuotaCeiling) : 'RPM 限'}
          </span>
        );
      },
    },
    { key: 'keyCount', title: 'Key 数量', width: 90, align: 'right', render: r => <span className="num">{r.keyCount}</span> },
    {
      key: 'createdAt',
      title: '创建时间',
      width: 150,
      render: r => {
        const d = dayjs(r.createdAt);
        return <span className="num muted">{d.isValid() ? d.format('YYYY-MM-DD HH:mm') : r.createdAt}</span>;
      },
    },
    {
      // 高频（充值）外露，低频（令牌上限/重置密码/删除）收进「更多」
      key: 'actions',
      title: '操作',
      align: 'right',
      width: 160,
      render: r => {
        const isSelf = r.id === me?.id;
        const isLastAdmin = r.role === 'admin' && adminCount <= 1;
        const isUser = r.role === 'user';
        const items: MenuItem[] = [
          ...(isUser ? [{ label: '令牌上限', icon: 'sliders' as const, onClick: () => setCeilingUser(r) }] : []),
          ...(!isSelf ? [{ label: '重置密码', icon: 'key' as const, onClick: () => setResetting(r) }] : []),
          ...(!isSelf && !isLastAdmin ? [{ sep: true as const }, { label: '删除', icon: 'x' as const, danger: true, onClick: () => void confirmDelete(r) }] : []),
        ];
        return (
          <HStack gap={4} style={{ justifyContent: 'flex-end' }}>
            <Button
              size="sm"
              disabled={!isUser}
              title={isUser ? undefined : '管理员无钱包'}
              onClick={() => setTopupUser(r)}
            >
              充值
            </Button>
            <Dropdown
              ariaLabel={`更多操作 ${r.username}`}
              title={isLastAdmin ? '最后一个管理员不可删除' : undefined}
              items={items}
            />
          </HStack>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader
        title="用户管理"
        desc="账号与角色；普通用户只能管理自己的访问令牌"
        extra={<Button variant="primary" onClick={() => setCreating(true)}>新建用户</Button>}
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
          ) : isLoading && users.length === 0 ? (
            <div style={{ padding: 14 }}>
              <SkLines rows={['', 'w80', 'w60', 'w80']} />
            </div>
          ) : users.length === 0 ? (
            <EmptyState
              title="还没有其他账号"
              desc="这里只有你自己。新建普通用户后，他们各自管理自己的访问令牌。"
              action={<Button size="sm" variant="primary" onClick={() => setCreating(true)}>新建用户</Button>}
            />
          ) : (
            <Table<UserAccount> columns={columns} rows={users} rowKey={r => r.id} />
          )}
        </BlockCard>
      </Blocks>

      <CreateUserModal open={creating} onCancel={() => setCreating(false)} onSubmit={create} />
      <ResetPasswordModal user={resetting} onCancel={() => setResetting(null)} onSubmit={resetPw} />
      <TopupModal user={topupUser} onCancel={() => setTopupUser(null)} onSubmit={topup} />
      <CeilingModal user={ceilingUser} onCancel={() => setCeilingUser(null)} onSubmit={saveCeiling} />
    </>
  );
}
