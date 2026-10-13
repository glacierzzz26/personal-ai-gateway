import { useEffect, useState } from 'react';
import { api } from '@/services/api';
import { Modal, Btn, Field, Input } from '@/ui/primitives';
import { toast } from '@/ui/toast';

type FormErrs = { oldPassword?: string; newPassword?: string; confirm?: string };

/** 改自己密码。成功后服务端重签会话 cookie,本机不掉线。 */
export default function ChangePasswordModal(props: { open: boolean; onClose: () => void }) {
  const { open, onClose } = props;
  const [oldPassword, setOldPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [errs, setErrs] = useState<FormErrs>({});
  const [saving, setSaving] = useState(false);

  /** 打开即清空(等价于原 antd Form 的 destroyOnHidden + resetFields)。 */
  useEffect(() => {
    if (open) {
      setOldPassword('');
      setNewPassword('');
      setConfirm('');
      setErrs({});
      setSaving(false);
    }
  }, [open]);

  /** 原 antd Form rules 逐条照搬为手写校验。 */
  const validate = (): boolean => {
    const e: FormErrs = {};
    if (!oldPassword) e.oldPassword = '请输入当前密码';
    if (!newPassword || newPassword.length < 8) e.newPassword = '至少 8 位';
    if (!confirm) e.confirm = '请再次输入新密码';
    else if (confirm !== newPassword) e.confirm = '两次输入不一致';
    setErrs(e);
    return Object.keys(e).length === 0;
  };

  const submit = async () => {
    if (!validate()) return;
    setSaving(true);
    try {
      await api.changePassword(oldPassword, newPassword);
      toast.ok('密码已更新');
      onClose();
    } catch (e) {
      const msg = e instanceof Error ? e.message : '请稍后重试';
      toast.err(`修改失败:${msg}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title="修改密码"
      open={open}
      onClose={onClose}
      width={440}
      footer={
        <>
          <Btn variant="secondary" onClick={onClose} disabled={saving}>取消</Btn>
          <Btn variant="primary" loading={saving} onClick={submit}>保存</Btn>
        </>
      }
    >
      <Field label="当前密码" err={errs.oldPassword} style={{ marginBottom: 12 }}>
        <Input
          type="password"
          autoComplete="current-password"
          value={oldPassword}
          onChange={e => setOldPassword(e.target.value)}
          err={!!errs.oldPassword}
        />
      </Field>
      <Field label="新密码" err={errs.newPassword} style={{ marginBottom: 12 }}>
        <Input
          type="password"
          autoComplete="new-password"
          value={newPassword}
          onChange={e => setNewPassword(e.target.value)}
          err={!!errs.newPassword}
        />
      </Field>
      <Field label="确认新密码" err={errs.confirm}>
        <Input
          type="password"
          autoComplete="new-password"
          value={confirm}
          onChange={e => setConfirm(e.target.value)}
          err={!!errs.confirm}
        />
      </Field>
    </Modal>
  );
}
