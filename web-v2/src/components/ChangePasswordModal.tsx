import { useEffect, useState } from 'react';
import { Icon } from '@/ui/icons';
import { Button, Field, Input } from '@/ui/primitives';
import { message } from '@/ui/toast';
import { api } from '@/services/api';

interface Errors {
  old?: string;
  neu?: string;
  confirm?: string;
}

/** 改自己密码。成功后服务端重签会话 cookie,本机不掉线。 */
export default function ChangePasswordModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [old, setOld] = useState('');
  const [neu, setNeu] = useState('');
  const [confirm, setConfirm] = useState('');
  const [err, setErr] = useState<Errors>({});
  const [saving, setSaving] = useState(false);

  // 每次打开清空,避免上次的输入/报错残留。
  useEffect(() => {
    if (open) {
      setOld('');
      setNeu('');
      setConfirm('');
      setErr({});
    }
  }, [open]);

  const submit = async () => {
    const e: Errors = {};
    if (!old) e.old = '请输入当前密码';
    if (!neu) e.neu = '请输入新密码';
    else if (neu.length < 8) e.neu = '至少 8 位';
    if (!confirm) e.confirm = '请再次输入新密码';
    else if (neu !== confirm) e.confirm = '两次输入不一致';
    setErr(e);
    if (e.old || e.neu || e.confirm) return;

    setSaving(true);
    try {
      await api.changePassword(old, neu);
      message.success('密码已更新');
      onClose();
    } catch (ex) {
      message.error(`修改失败:${ex instanceof Error ? ex.message : '请稍后重试'}`);
    } finally {
      setSaving(false);
    }
  };

  if (!open) return null;

  return (
    <div
      className="overlay"
      style={{ zIndex: 80 }}
      onMouseDown={e => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal" style={{ maxWidth: 440 }}>
        <div className="modal-head">
          <div style={{ flex: 1, minWidth: 0 }}>
            <h3>修改密码</h3>
          </div>
          <button className="icon-btn" onClick={onClose} aria-label="关闭">
            <Icon name="x" size={13} />
          </button>
        </div>
        <div className="modal-body">
          <div className="stack">
            <Field label="当前密码" error={err.old}>
              <Input
                type="password"
                value={old}
                invalid={!!err.old}
                autoComplete="current-password"
                onChange={e => {
                  setOld(e.target.value);
                  if (err.old) setErr(s => ({ ...s, old: undefined }));
                }}
              />
            </Field>
            <Field label="新密码" error={err.neu} hint={err.neu ? undefined : '至少 8 位'}>
              <Input
                type="password"
                value={neu}
                invalid={!!err.neu}
                autoComplete="new-password"
                onChange={e => {
                  setNeu(e.target.value);
                  if (err.neu) setErr(s => ({ ...s, neu: undefined }));
                }}
              />
            </Field>
            <Field label="确认新密码" error={err.confirm}>
              <Input
                type="password"
                value={confirm}
                invalid={!!err.confirm}
                autoComplete="new-password"
                onChange={e => {
                  setConfirm(e.target.value);
                  if (err.confirm) setErr(s => ({ ...s, confirm: undefined }));
                }}
                onKeyDown={e => {
                  if (e.key === 'Enter') void submit();
                }}
              />
            </Field>
          </div>
        </div>
        <div className="modal-foot">
          <Button variant="secondary" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={saving} onClick={() => void submit()}>
            保存
          </Button>
        </div>
      </div>
    </div>
  );
}
