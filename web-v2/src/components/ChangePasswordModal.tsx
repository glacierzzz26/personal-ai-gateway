import { useEffect, useState } from 'react';
import { App, Form, Input, Modal } from 'antd';
import { api } from '@/services/api';

/** 改自己密码。成功后服务端重签会话 cookie,本机不掉线。 */
export default function ChangePasswordModal(props: { open: boolean; onClose: () => void }) {
  const { open, onClose } = props;
  const { message } = App.useApp();
  const [form] = Form.useForm<{ oldPassword: string; newPassword: string; confirm: string }>();
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (open) form.resetFields();
  }, [open, form]);

  const submit = async () => {
    const v = await form.validateFields();
    setSaving(true);
    try {
      await api.changePassword(v.oldPassword, v.newPassword);
      message.success('密码已更新');
      onClose();
    } catch (e) {
      const msg = e instanceof Error ? e.message : '请稍后重试';
      message.error(`修改失败:${msg}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title="修改密码"
      open={open}
      onCancel={onClose}
      onOk={submit}
      confirmLoading={saving}
      okText="保存"
      cancelText="取消"
      destroyOnHidden
      width={440}
    >
      <Form form={form} layout="vertical" requiredMark={false} preserve={false}>
        <Form.Item
          name="oldPassword" label="当前密码"
          rules={[{ required: true, message: '请输入当前密码' }]}
        >
          <Input.Password autoComplete="current-password" />
        </Form.Item>
        <Form.Item
          name="newPassword" label="新密码"
          rules={[{ required: true, min: 8, message: '至少 8 位' }]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
        <Form.Item
          name="confirm" label="确认新密码"
          dependencies={['newPassword']}
          rules={[
            { required: true, message: '请再次输入新密码' },
            ({ getFieldValue }) => ({
              validator(_, value) {
                if (!value || getFieldValue('newPassword') === value) return Promise.resolve();
                return Promise.reject(new Error('两次输入不一致'));
              },
            }),
          ]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
      </Form>
    </Modal>
  );
}
