import { useEffect, useState } from 'react';
import { App, Button, Card, Form, Input, Typography } from 'antd';
import { LockOutlined, UserOutlined } from '@ant-design/icons';
import { useSession } from '@/stores/session';
import { api } from '@/services/api';

type Mode = 'loading' | 'login' | 'bootstrap';

/** 登录 / 首启创建管理员。SessionGate 在未登录态渲染本页。 */
export default function Login() {
  const { message } = App.useApp();
  const setAdmin = useSession(s => s.setAdmin);
  const [mode, setMode] = useState<Mode>('loading');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let live = true;
    api.authState()
      .then(st => { if (live) setMode(st.adminExists ? 'login' : 'bootstrap'); })
      .catch(() => { if (live) setMode('login'); });
    return () => { live = false; };
  }, []);

  const finish = async (values: { username: string; password: string }) => {
    setSubmitting(true);
    try {
      const admin = mode === 'bootstrap'
        ? await api.bootstrap(values.username, values.password)
        : await api.login(values.username, values.password);
      setAdmin(admin);
      message.success(mode === 'bootstrap' ? '管理员已创建并登录' : '欢迎回来');
    } catch (e) {
      message.error(e instanceof Error ? e.message : '登录失败');
    } finally {
      setSubmitting(false);
    }
  };

  const isBootstrap = mode === 'bootstrap';

  return (
    <div
      style={{
        minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: 'var(--gw-fill)', padding: 16,
      }}
    >
      <Card style={{ width: 380, borderRadius: 12 }} styles={{ body: { padding: '28px 32px' } }}>
        <div style={{ textAlign: 'center', marginBottom: 8 }}>
          <div
            style={{
              width: 40, height: 40, margin: '0 auto 12px', borderRadius: 10,
              background: 'var(--gw-primary)', color: '#fff', fontSize: 20,
              display: 'flex', alignItems: 'center', justifyContent: 'center', fontWeight: 700,
            }}
          >
            G
          </div>
          <Typography.Title level={4} style={{ margin: 0 }}>
            {isBootstrap ? '创建管理员' : 'AI Gateway 管理台'}
          </Typography.Title>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            {isBootstrap
              ? '首次运行：创建一个管理员账号'
              : '使用管理员账号登录，会话 Cookie 自动保持'}
          </Typography.Text>
        </div>

        <Form layout="vertical" onFinish={finish} style={{ marginTop: 20 }} requiredMark={false}>
          <Form.Item
            name="username"
            label="用户名"
            rules={[{ required: true, message: '请输入用户名' }]}
          >
            <Input prefix={<UserOutlined />} placeholder="admin" autoComplete="username" />
          </Form.Item>
          <Form.Item
            name="password"
            label="密码"
            rules={[
              { required: true, message: '请输入密码' },
              ...(isBootstrap ? [{ min: 8, message: '至少 8 位' }] : []),
            ]}
          >
            <Input.Password prefix={<LockOutlined />} placeholder={isBootstrap ? '至少 8 位' : '••••••••'} autoComplete="current-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={submitting} style={{ marginTop: 4 }}>
            {isBootstrap ? '创建并进入' : '登 录'}
          </Button>
        </Form>
      </Card>
    </div>
  );
}
