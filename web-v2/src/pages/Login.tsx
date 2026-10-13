import { useEffect, useState } from 'react';
import { useSession } from '@/stores/session';
import { api } from '@/services/api';
import { Button, Field, Input, Note, Sk } from '@/ui/primitives';
import { message } from '@/ui/toast';

type Mode = 'loading' | 'login' | 'bootstrap';

/** 登录页 brand 标记（38px 版，原型内联 SVG）。 */
function AuthMark() {
  return (
    <div
      className="brand-mark"
      style={{ width: 38, height: 38, margin: '0 auto 10px', fontSize: 16, fontWeight: 600 }}
    >
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.8}
        strokeLinecap="round"
        strokeLinejoin="round"
        style={{ width: 22, height: 22 }}
      >
        <circle cx="6" cy="19" r="2.4" />
        <circle cx="18" cy="5" r="2.4" />
        <path d="M8.4 19h7.9a3.4 3.4 0 0 0 0-6.8H7.7a3.4 3.4 0 0 1 0-6.8H15" />
      </svg>
    </div>
  );
}

/** 登录 / 首启创建管理员。App 在未登录态渲染本页。 */
export default function Login() {
  const setAdmin = useSession(s => s.setAdmin);
  const [mode, setMode] = useState<Mode>('loading');
  const [submitting, setSubmitting] = useState(false);
  const [user, setUser] = useState('');
  const [pass, setPass] = useState('');
  const [err, setErr] = useState<{ user?: string; pass?: string }>({});
  /** 探测登录状态失败：仍按登录页渲染，但必须告知用户，否则「没建过账号」会被误判成「连不上」 */
  const [probeFailed, setProbeFailed] = useState(false);

  useEffect(() => {
    let live = true;
    api
      .authState()
      .then(st => {
        if (live) setMode(st.adminExists ? 'login' : 'bootstrap');
      })
      .catch(() => {
        if (!live) return;
        setMode('login');
        setProbeFailed(true);
      });
    return () => {
      live = false;
    };
  }, []);

  const isBootstrap = mode === 'bootstrap';

  const finish = async () => {
    const u = user.trim();
    const e: { user?: string; pass?: string } = {};
    if (!u) e.user = '请输入用户名';
    if (!pass) e.pass = '请输入密码';
    else if (isBootstrap && pass.length < 8) e.pass = '密码至少 8 位';
    setErr(e);
    if (e.user || e.pass) return;

    setSubmitting(true);
    try {
      const admin = isBootstrap ? await api.bootstrap(u, pass) : await api.login(u, pass);
      setAdmin(admin);
      message.success(isBootstrap ? '管理员已创建并登录' : '欢迎回来');
    } catch (ex) {
      message.error(ex instanceof Error ? ex.message : '登录失败');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div
      style={{
        height: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: 'var(--bg)',
        padding: 20,
      }}
    >
      <div className="card" style={{ width: 400, padding: '28px 28px 22px' }}>
        {/* 判定首启还是登录前，不渲染表单，避免用户对着空表单输入 */}
        {mode === 'loading' ? (
          <>
            <AuthMark />
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8, alignItems: 'center' }}>
              <Sk w={180} h={12} />
              <Sk w={120} h={12} />
            </div>
            <span className="cell-sub" role="status" style={{ display: 'block', textAlign: 'center', marginTop: 10 }}>
              正在读取登录状态
            </span>
          </>
        ) : (
          <>
            <div style={{ textAlign: 'center', marginBottom: 18 }}>
              <AuthMark />
              <h1 style={{ fontSize: 18, marginBottom: 5 }}>{isBootstrap ? '创建管理员' : 'AI Gateway 管理台'}</h1>
              <div className="cell-sub" style={{ fontSize: 12 }}>
                {isBootstrap ? '首次运行：创建一个管理员账号' : '使用管理员账号登录，会话 Cookie 自动保持'}
              </div>
            </div>

            {probeFailed && (
              <div style={{ marginBottom: 12 }}>
                <Note tone="warn">
                  <b>无法确认登录状态</b>
                  <div>探测 /auth/state 失败，可能是网关管理面未启动。你仍然可以尝试登录。</div>
                </Note>
              </div>
            )}

            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <Field label="用户名" error={err.user}>
                <Input
                  value={user}
                  invalid={!!err.user}
                  placeholder="admin"
                  autoComplete="username"
                  onChange={e => {
                    setUser(e.target.value);
                    if (err.user) setErr(s => ({ ...s, user: undefined }));
                  }}
                />
              </Field>

              <Field label="密码" error={err.pass}>
                <Input
                  type="password"
                  value={pass}
                  invalid={!!err.pass}
                  placeholder={isBootstrap ? '至少 8 位' : '••••••••'}
                  autoComplete={isBootstrap ? 'new-password' : 'current-password'}
                  onChange={e => {
                    setPass(e.target.value);
                    if (err.pass) setErr(s => ({ ...s, pass: undefined }));
                  }}
                  onKeyDown={e => {
                    if (e.key === 'Enter') void finish();
                  }}
                />
              </Field>

              <Button
                variant="primary"
                loading={submitting}
                onClick={() => void finish()}
                style={{ width: '100%', marginTop: 4, height: 36, fontSize: 14 }}
              >
                {isBootstrap ? '创建并进入' : '登录'}
              </Button>
            </div>

            <div className="cell-sub" style={{ textAlign: 'center', marginTop: 14, fontSize: 11 }}>
              {isBootstrap ? '账号创建后即成为唯一管理员' : '忘记密码请联系管理员重置'}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
