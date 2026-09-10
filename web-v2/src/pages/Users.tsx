import { useState } from 'react';
import { App, Button, Card, Form, Input, Modal, Select, Space, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import PageHeader from '@/components/PageHeader';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import type { Role, UserAccount } from '@/types';

const errMsg = (e: unknown) => (e instanceof Error ? e.message : '请稍后重试');

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

export default function Users() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const me = useSession(s => s.admin);
  const [creating, setCreating] = useState(false);
  const [resetting, setResetting] = useState<UserAccount | null>(null);

  const { data: users = [], isLoading } = useQuery({ queryKey: ['users'], queryFn: api.getUsers });
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
    { title: '用户名', dataIndex: 'username', render: v => <b style={{ fontWeight: 500 }}>{v}</b> },
    {
      title: '角色', dataIndex: 'role', width: 120,
      render: v => (v === 'admin' ? <Tag color="blue">管理员</Tag> : <Tag>普通用户</Tag>),
    },
    { title: 'Key 数量', dataIndex: 'keyCount', width: 100, align: 'right', render: v => <span className="gw-num">{v}</span> },
    {
      title: '创建时间', dataIndex: 'createdAt', width: 180,
      render: v => {
        const d = dayjs(v);
        return <span style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>{d.isValid() ? d.format('YYYY-MM-DD HH:mm') : v}</span>;
      },
    },
    {
      title: '', align: 'right', width: 180,
      render: (_, r) => {
        const isSelf = r.id === me?.id;
        const isLastAdmin = r.role === 'admin' && adminCount <= 1;
        return (
          <Space size={4}>
            <Button size="small" disabled={isSelf} onClick={() => setResetting(r)}>重置密码</Button>
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
        desc="账号与角色;普通用户只能管理自己的访问令牌"
        extra={<Button type="primary" onClick={() => setCreating(true)}>新建用户</Button>}
      />

      <Card>
        <Table<UserAccount>
          rowKey="id"
          size="middle"
          loading={isLoading}
          dataSource={users}
          columns={columns}
          pagination={false}
        />
      </Card>

      <CreateUserModal open={creating} onCancel={() => setCreating(false)} onSubmit={create} />
      <ResetPasswordModal user={resetting} onCancel={() => setResetting(null)} onSubmit={resetPw} />
    </div>
  );
}
