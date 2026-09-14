import { useState } from 'react';
import { App, Button, Form, Input, InputNumber, Modal, Select, Space, Table, Tooltip } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import { EmptyState, ErrorState } from '@/components/States';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { fmt } from '@/utils/format';
import type { Role, UserAccount } from '@/types';

const errMsg = (e: unknown) => (e instanceof Error ? e.message : '请稍后重试');

/** 角色徽章：文字 + 语义色，不靠颜色单独表意。 */
function RoleBadge({ role }: { role: Role }) {
  const admin = role === 'admin';
  return (
    <span className={`gw-badge${admin ? ' tint' : ''}`} style={{ fontSize: 13 }}>
      {admin ? '管理员' : '普通用户'}
    </span>
  );
}

/** 新建用户弹窗(角色默认普通用户)。 */
function CreateUserModal(props: {
  open: boolean;
  onCancel: () => void;
  onSubmit: (v: { username: string; password: string; role: Role }) => Promise<void>;
}) {
  const { open, onCancel, onSubmit } = props;
  const { message } = App.useApp();
  const [form] = Form.useForm<{ username: string; password: string; role: Role }>();
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    const v = await form.validateFields();
    setSaving(true);
    try {
      await onSubmit({ username: v.username.trim(), password: v.password, role: v.role });
      form.resetFields();
    } catch (e) {
      message.error(`创建失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title="新建用户"
      open={open}
      onCancel={onCancel}
      onOk={submit}
      confirmLoading={saving}
      okText="创建"
      cancelText="取消"
      destroyOnHidden
      width={440}
    >
      <Form form={form} layout="vertical" requiredMark={false} preserve={false} initialValues={{ role: 'user' }}>
        <Form.Item name="username" label="用户名" rules={[{ required: true, whitespace: true, message: '请输入用户名' }]}>
          <Input placeholder="例如:alice" maxLength={64} />
        </Form.Item>
        <Form.Item name="password" label="初始密码" rules={[{ required: true, min: 8, message: '至少 8 位' }]}>
          <Input.Password placeholder="至少 8 位,告知用户后由其自行修改" autoComplete="new-password" />
        </Form.Item>
        <Form.Item name="role" label="角色">
          <Select
            options={[
              { value: 'user', label: '普通用户(仅能管理自己的访问令牌)' },
              { value: 'admin', label: '管理员(全部权限)' },
            ]}
          />
        </Form.Item>
      </Form>
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
  const { message } = App.useApp();
  const [form] = Form.useForm<{ newPassword: string }>();
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    const v = await form.validateFields();
    if (!user) return;
    setSaving(true);
    try {
      await onSubmit(user.id, v.newPassword);
      form.resetFields();
    } catch (e) {
      message.error(`重置失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={user ? `重置「${user.username}」的密码` : '重置密码'}
      open={!!user}
      onCancel={onCancel}
      onOk={submit}
      confirmLoading={saving}
      okText="重置"
      cancelText="取消"
      destroyOnHidden
      width={440}
    >
      <Form form={form} layout="vertical" requiredMark={false} preserve={false}>
        <Form.Item name="newPassword" label="新密码" rules={[{ required: true, min: 8, message: '至少 8 位' }]}>
          <Input.Password autoComplete="new-password" />
        </Form.Item>
      </Form>
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
  const { message } = App.useApp();
  const [form] = Form.useForm<{ amount: number; note?: string }>();
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    const v = await form.validateFields();
    if (!user) return;
    setSaving(true);
    try {
      await onSubmit(user.id, v.amount, v.note?.trim() || undefined);
      form.resetFields();
    } catch (e) {
      message.error(`充值失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={user ? `调整「${user.username}」的余额` : '调整余额'}
      open={!!user}
      onCancel={onCancel}
      onOk={submit}
      confirmLoading={saving}
      okText="提交"
      cancelText="取消"
      destroyOnHidden
      width={440}
    >
      <Form form={form} layout="vertical" requiredMark={false} preserve={false} initialValues={{ amount: 100 }}>
        {user && (
          <div style={{ marginBottom: 16, fontSize: 13, color: 'var(--gw-text-3)' }}>
            当前余额:<b className="gw-num" style={{ color: 'var(--gw-text)' }}>{fmt.usd(user.balanceUsd)}</b>
          </div>
        )}
        <Form.Item
          name="amount" label="金额(正=充值,负=扣减)"
          rules={[{ required: true, message: '请输入金额' }]}
        >
          <InputNumber style={{ width: '100%' }} precision={2} step={100} placeholder="例如 100 或 -50" />
        </Form.Item>
        <Form.Item name="note" label="备注(可选)">
          <Input placeholder="例如:微信转账 / 试用额度" maxLength={120} />
        </Form.Item>
      </Form>
    </Modal>
  );
}

/** 售价倍率弹窗:留空 = 回落全局倍率。 */
function RateModal(props: {
  user: UserAccount | null;
  globalRate: number;
  onCancel: () => void;
  onSubmit: (id: number, rate: number | null) => Promise<void>;
}) {
  const { user, globalRate, onCancel, onSubmit } = props;
  const { message } = App.useApp();
  const [form] = Form.useForm<{ rate: number | null }>();
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    const v = await form.validateFields();
    if (!user) return;
    setSaving(true);
    try {
      await onSubmit(user.id, v.rate ?? null);
      form.resetFields();
    } catch (e) {
      message.error(`保存失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={user ? `设置「${user.username}」的售价倍率` : '售价倍率'}
      open={!!user}
      onCancel={onCancel}
      onOk={submit}
      confirmLoading={saving}
      okText="保存"
      cancelText="取消"
      destroyOnHidden
      width={440}
    >
      <Form form={form} layout="vertical" requiredMark={false} preserve={false} initialValues={{ rate: user?.rateOverride ?? null }}>
        <div style={{ marginBottom: 16, fontSize: 13, color: 'var(--gw-text-3)' }}>
          本站价 = 官方价 × 倍率。留空则跟随全局倍率(当前 <b className="gw-num" style={{ color: 'var(--gw-text)' }}>×{globalRate}</b>)。
        </div>
        <Form.Item
          name="rate" label="倍率覆盖"
          rules={[{ type: 'number', min: 0.01, message: '需为正数' }]}
        >
          <InputNumber style={{ width: '100%' }} precision={2} step={0.1} min={0.01} placeholder="留空=用全局倍率" />
        </Form.Item>
      </Form>
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
  const { message } = App.useApp();
  const [form] = Form.useForm<{ quotaUsd: number; rpmLimit: number }>();
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    const v = await form.validateFields();
    if (!user) return;
    setSaving(true);
    try {
      await onSubmit(user.id, v.quotaUsd ?? 0, v.rpmLimit ?? 0);
      form.resetFields();
    } catch (e) {
      message.error(`保存失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={user ? `设置「${user.username}」的令牌上限` : '令牌上限'}
      open={!!user}
      onCancel={onCancel}
      onOk={submit}
      confirmLoading={saving}
      okText="保存"
      cancelText="取消"
      destroyOnHidden
      width={440}
    >
      <Form
        form={form}
        layout="vertical"
        requiredMark={false}
        preserve={false}
        initialValues={{ quotaUsd: 0, rpmLimit: 0 }}
      >
        <div style={{ marginBottom: 16, fontSize: 13, color: 'var(--gw-text-3)' }}>
          该客户自助创建的令牌不得超过此上限(0 = 不限)。真正常住闸门是余额,这里是令牌子预算的天花板。
        </div>
        <Form.Item
          name="quotaUsd" label="令牌额度上限"
          extra="单位同计价币种;0 = 不限"
        >
          <InputNumber min={0} precision={2} step={10} style={{ width: '100%' }} placeholder="0 = 不限" />
        </Form.Item>
        <Form.Item name="rpmLimit" label="令牌 RPM 上限" extra="0 = 不限">
          <InputNumber min={0} max={100000} style={{ width: '100%' }} placeholder="0 = 不限" />
        </Form.Item>
      </Form>
    </Modal>
  );
}

export default function Users() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const me = useSession(s => s.admin);
  const [creating, setCreating] = useState(false);
  const [resetting, setResetting] = useState<UserAccount | null>(null);
  const [topupUser, setTopupUser] = useState<UserAccount | null>(null);
  const [rateUser, setRateUser] = useState<UserAccount | null>(null);
  const [ceilingUser, setCeilingUser] = useState<UserAccount | null>(null);

  const { data: users = [], isLoading, isError, refetch } = useQuery({ queryKey: ['users'], queryFn: api.getUsers });
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.getSettings });
  const globalRate = settings?.priceMultiplier && settings.priceMultiplier > 0 ? settings.priceMultiplier : 1;
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

  const saveRate = async (id: number, rate: number | null) => {
    await api.setUserRate(id, rate);
    message.success('倍率已保存');
    setRateUser(null);
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

  const confirmDelete = (u: UserAccount) => {
    modal.confirm({
      title: `删除用户「${u.username}」?`,
      content: `该用户名下 ${u.keyCount} 个访问令牌将一并删除且立即失效;历史日志中的令牌名称保留。`,
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => remove.mutateAsync(u.id),
    });
  };

  const columns: ColumnsType<UserAccount> = [
    { title: '用户名', dataIndex: 'username', width: 160, render: v => <b style={{ fontWeight: 500, color: 'var(--gw-text)' }}>{v}</b> },
    { title: '角色', dataIndex: 'role', width: 120, render: v => <RoleBadge role={v} /> },
    {
      title: '余额', dataIndex: 'balanceUsd', width: 130, align: 'right',
      render: (v: number, r) =>
        r.role !== 'user' ? (
          <span style={{ color: 'var(--gw-text-3)' }}>—</span>
        ) : (
          <span className="gw-num" style={{ color: v <= 0 ? 'var(--gw-err)' : undefined }}>{fmt.usd(v)}</span>
        ),
    },
    {
      title: '售价倍率', key: 'rate', width: 110, align: 'right',
      render: (_, r) => {
        if (r.role !== 'user') return <span style={{ color: 'var(--gw-text-3)' }}>—</span>;
        const own = r.rateOverride != null;
        return (
          <Tooltip title={own ? `覆盖全局倍率(全局 ×${globalRate})` : '跟随全局倍率'}>
            <span className="gw-num" style={{ color: own ? 'var(--gw-text)' : 'var(--gw-text-3)' }}>
              ×{own ? r.rateOverride : globalRate}{own ? '' : '(全局)'}
            </span>
          </Tooltip>
        );
      },
    },
    {
      title: '令牌上限', key: 'ceiling', width: 130, align: 'right',
      render: (_, r) => {
        if (r.role !== 'user') return <span style={{ color: 'var(--gw-text-3)' }}>—</span>;
        if (r.tokenQuotaCeiling <= 0 && r.tokenRpmCeiling <= 0) {
          return <span style={{ color: 'var(--gw-text-3)' }}>不限</span>;
        }
        return (
          <Tooltip title={`RPM 上限 ${r.tokenRpmCeiling > 0 ? r.tokenRpmCeiling : '不限'}`}>
            <span className="gw-num">
              {r.tokenQuotaCeiling > 0 ? fmt.usd(r.tokenQuotaCeiling) : 'RPM 限'}
            </span>
          </Tooltip>
        );
      },
    },
    { title: 'Key 数量', dataIndex: 'keyCount', width: 100, align: 'right', render: v => <span className="gw-num">{v}</span> },
    {
      title: '创建时间', dataIndex: 'createdAt', width: 170,
      render: v => {
        const d = dayjs(v);
        return <span className="gw-num" style={{ color: 'var(--gw-text-3)' }}>{d.isValid() ? d.format('YYYY-MM-DD HH:mm') : v}</span>;
      },
    },
    {
      title: '操作', align: 'right', width: 300,
      render: (_, r) => {
        const isSelf = r.id === me?.id;
        const isLastAdmin = r.role === 'admin' && adminCount <= 1;
        const isUser = r.role === 'user';
        return (
          <Space size={4}>
            <Button size="small" disabled={!isUser} title={isUser ? undefined : '管理员无钱包'}
              onClick={() => setTopupUser(r)}>
              充值
            </Button>
            <Button size="small" disabled={!isUser} title={isUser ? undefined : '管理员无售价倍率'}
              onClick={() => setRateUser(r)}>
              倍率
            </Button>
            <Button size="small" disabled={!isUser} title={isUser ? undefined : '管理员建令牌不受限'}
              onClick={() => setCeilingUser(r)}>
              令牌上限
            </Button>
            <Button size="small" disabled={isSelf} title={isSelf ? '不能重置自己的密码，请用右上角菜单' : undefined}
              onClick={() => setResetting(r)}>
              重置密码
            </Button>
            <Button
              size="small" danger
              disabled={isSelf || isLastAdmin}
              title={isSelf ? '不能删除自己' : isLastAdmin ? '不能删除最后一个管理员' : undefined}
              onClick={() => confirmDelete(r)}
            >
              删除
            </Button>
          </Space>
        );
      },
    },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="用户管理"
        desc="账号与角色；普通用户只能管理自己的访问令牌"
        extra={<Button type="primary" onClick={() => setCreating(true)}>新建用户</Button>}
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
            <Table<UserAccount>
              rowKey="id"
              size="middle"
              loading={isLoading && users.length === 0}
              dataSource={users}
              columns={columns}
              pagination={false}
              scroll={{ x: 820 }}
              locale={{
                emptyText: (
                  <EmptyState
                    title="还没有其他账号"
                    desc="这里只有你自己。新建普通用户后，他们各自管理自己的访问令牌。"
                    action={<Button size="small" type="primary" onClick={() => setCreating(true)}>新建用户</Button>}
                  />
                ),
              }}
            />
          )}
        </BlockCard>
      </Blocks>

      <CreateUserModal open={creating} onCancel={() => setCreating(false)} onSubmit={create} />
      <ResetPasswordModal user={resetting} onCancel={() => setResetting(null)} onSubmit={resetPw} />
      <TopupModal user={topupUser} onCancel={() => setTopupUser(null)} onSubmit={topup} />
      <RateModal user={rateUser} globalRate={globalRate} onCancel={() => setRateUser(null)} onSubmit={saveRate} />
      <CeilingModal user={ceilingUser} onCancel={() => setCeilingUser(null)} onSubmit={saveCeiling} />
    </div>
  );
}
