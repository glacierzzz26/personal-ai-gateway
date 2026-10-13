import { useEffect, useState } from 'react';
import { App, Button, Card, Form, Input } from 'antd';
import { LockOutlined, UserOutlined } from '@ant-design/icons';
import { useSession } from '@/stores/session';
import { api } from '@/services/api';
import { SkLines } from '@/components/States';
import { TOKENS } from '@/styles/tokens';

type Mode = 'loading' | 'login' | 'bootstrap';

/** 登录 / 首启创建管理员。App 在未登录态渲染本页。 */
export default function Login() {
  const { message } = App.useApp();
  const setAdmin = useSession(s => s.setAdmin);
  const [mode, setMode] = useState<Mode>('loading');
  const [submitting, setSubmitting] = useState(false);
  /** 探测登录状态失败：仍按登录页渲染，但必须告知用户，否则「没建过账号」会被误判成「连不上」 */
  const [probeFailed, setProbeFailed] = useState(false);

  useEffect(() => {
    let live = true;
    api.authState()
      .then(st => { if (live) setMode(st.adminExists ? 'login' : 'bootstrap'); })
      .catch(() => {
        if (!live) return;
        setMode('login');
        setProbeFailed(true);
      });
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
    <div className="gw-auth">
      <Card className="gw-auth-card">
        {/* 判定首启还是登录前，不渲染表单，避免用户对着空表单输入 */}
        {mode === 'loading' ? (
          <>
            <div className="gw-auth-brand">
              <span className="gw-brand-mark lg" aria-hidden="true">G</span>
              <div style={{ width: 180, marginTop: 4 }}>
                <SkLines rows={['', 'w60']} />
              </div>
            </div>
            <span className="gw-sr" role="status">正在读取登录状态</span>
          </>
        ) : (
          <>
            <div className="gw-auth-brand">
              <span className="gw-brand-mark lg" aria-hidden="true">G</span>
              <h1>{isBootstrap ? '创建管理员' : 'AI Gateway 管理台'}</h1>
              <p>
                {isBootstrap
                  ? '首次运行：创建一个管理员账号'
                  : '使用管理员账号登录，会话 Cookie 自动保持'}
              </p>
            </div>

            {probeFailed && (
              <div className="gw-note" role="status" style={{ borderLeftColor: TOKENS.warn, background: 'var(--gw-card)', marginBottom: 4 }}>
                <b style={{ color: TOKENS.warn }}>⚠ 无法确认登录状态</b>
                <span style={{ flex: 1 }}>
                  探测 /auth/state 失败，可能是网关管理面未启动。你仍然可以尝试登录。
                </span>
              </div>
            )}

            <Form layout="vertical" onFinish={finish} requiredMark={false}>
              <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
                <Input prefix={<UserOutlined />} placeholder="admin" autoComplete="username" size="large" />
              </Form.Item>
              <Form.Item
                name="password"
                label="密码"
                rules={[
                  { required: true, message: '请输入密码' },
                  ...(isBootstrap ? [{ min: 8, message: '至少 8 位' }] : []),
                ]}
              >
                <Input.Password
                  prefix={<LockOutlined />}
                  placeholder={isBootstrap ? '至少 8 位' : '••••••••'}
                  autoComplete={isBootstrap ? 'new-password' : 'current-password'}
                  size="large"
                />
              </Form.Item>
              <Button type="primary" htmlType="submit" block size="large" loading={submitting}>
                {isBootstrap ? '创建并进入' : '登录'}
              </Button>
            </Form>

            <div className="gw-auth-foot">
              {isBootstrap ? '账号创建后即成为唯一管理员' : '忘记密码请联系管理员重置'}
            </div>
          </>
        )}
      </Card>
    </div>
  );
}
