import { useEffect, useState } from 'react';
import { useSession } from '@/stores/session';
import { api } from '@/services/api';
import { SkLines } from '@/components/States';
import { Btn, Field, Input, Note } from '@/ui/primitives';
import { toast } from '@/ui/toast';

type Mode = 'loading' | 'login' | 'bootstrap';

/** 登录 / 首启创建管理员。App 在未登录态渲染本页（对齐原型 showLogin）。 */
export default function Login() {
  const setAdmin = useSession(s => s.setAdmin);
  const [mode, setMode] = useState<Mode>('loading');
  const [submitting, setSubmitting] = useState(false);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [err, setErr] = useState('');
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

  const submit = async () => {
    const u = username.trim();
    const isBootstrap = mode === 'bootstrap';
    if (!u || !password || (isBootstrap && password.length < 8)) {
      setErr(isBootstrap ? '请填写用户名与密码，密码至少 8 位' : '请填写用户名与密码');
      return;
    }
    setErr('');
    setSubmitting(true);
    try {
      const admin = isBootstrap
        ? await api.bootstrap(u, password)
        : await api.login(u, password);
      setAdmin(admin);
      toast.ok(isBootstrap ? '管理员已创建并登录' : '欢迎回来');
    } catch (e) {
      toast.err(e instanceof Error ? e.message : '登录失败');
    } finally {
      setSubmitting(false);
    }
  };

  const isBootstrap = mode === 'bootstrap';

  return (
    <div style={{ height: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--bg)' }}>
      <div className="card" style={{ width: 400, padding: '28px 28px 22px' }}>
        {mode === 'loading' ? (
          <>
            <div style={{ textAlign: 'center', marginBottom: 18 }}>
              <div className="brand-mark" style={{ width: 38, height: 38, margin: '0 auto 10px', fontSize: 16, fontWeight: 600 }}>G</div>
              <div style={{ width: 180, margin: '4px auto 0' }}>
                <SkLines rows={['', 'w60']} />
              </div>
            </div>
            <span role="status" style={{ position: 'absolute', left: -9999 }}>正在读取登录状态</span>
          </>
        ) : (
          <>
            <div style={{ textAlign: 'center', marginBottom: 18 }}>
              <div className="brand-mark" style={{ width: 38, height: 38, margin: '0 auto 10px', fontSize: 16, fontWeight: 600 }}>G</div>
              <h1 style={{ fontSize: 18, marginBottom: 5 }}>{isBootstrap ? '创建管理员' : 'AI Gateway 管理台'}</h1>
              <div className="cell-sub" style={{ fontSize: 12 }}>
                {isBootstrap ? '首次运行：创建一个管理员账号' : '使用管理员账号登录，会话 Cookie 自动保持'}
              </div>
            </div>

            {probeFailed && (
              <div style={{ marginBottom: 12 }}>
                <Note tone="warn">
                  <b style={{ color: 'var(--warn)' }}>⚠ 无法确认登录状态</b>　探测 /auth/state 失败，可能是网关管理面未启动。你仍然可以尝试登录。
                </Note>
              </div>
            )}

            <form
              onSubmit={e => { e.preventDefault(); void submit(); }}
              onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); void submit(); } }}
            >
              <Field label="用户名" style={{ marginBottom: 12 }}>
                <Input placeholder="admin" autoComplete="username" value={username} onChange={e => setUsername(e.target.value)} err={!!err && !username.trim()} />
              </Field>
              <Field label="密码" err={err || undefined}>
                <Input
                  type="password"
                  placeholder={isBootstrap ? '至少 8 位' : '••••••••'}
                  autoComplete={isBootstrap ? 'new-password' : 'current-password'}
                  value={password}
                  onChange={e => setPassword(e.target.value)}
                  err={!!err && (!password || (isBootstrap && password.length < 8))}
                />
              </Field>
              <Btn variant="primary" type="submit" loading={submitting} style={{ width: '100%', marginTop: 12, height: 36, fontSize: 14 }}>
                {isBootstrap ? '创建并进入' : '登录'}
              </Btn>
            </form>

            <div className="cell-sub" style={{ textAlign: 'center', marginTop: 14, fontSize: 11 }}>
              {isBootstrap ? '账号创建后即成为唯一管理员' : '忘记密码请联系管理员重置'}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
