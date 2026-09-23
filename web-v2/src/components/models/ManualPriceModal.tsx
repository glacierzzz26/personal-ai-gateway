import { useEffect, useMemo, useRef } from 'react';
import { App, Form, Input, InputNumber, Modal, Select, Space } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/services/api';
import type { ManualPriceDraft, Provider } from '@/types';

interface Props {
  open: boolean;
  /** 预填厂商(来自当前模型/渠道上下文);缺省取厂商列表首项 */
  defaultProvider?: Provider;
  /** 预填模型名(渠道侧真实名) */
  defaultModelName?: string;
  /** 厂商下拉可选值;缺省为「全部有官方来源的厂商」 */
  providerOptions?: Provider[];
  onClose: () => void;
}

/**
 * 手工录入官方参考价(页面动态渲染、无法稳定抓取的厂商)。
 * 官方价与手工报价分表:此处只写 official_prices,不动任何 offer。
 */
export default function ManualPriceModal({
  open, defaultProvider, defaultModelName, providerOptions, onClose,
}: Props) {
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<ManualPriceDraft>();

  const { data: vendors = [] } = useQuery({
    queryKey: ['official-vendors'],
    queryFn: () => api.officialVendors(),
    retry: 0,
    staleTime: 300_000,
  });
  const options = useMemo(
    () => providerOptions ?? vendors.map(v => v.provider),
    [providerOptions, vendors],
  );

  const provider = Form.useWatch('provider', form);
  // 默认原币随厂商:Anthropic/OpenAI/Azure 官网标美元,国内厂商标人民币。
  // 用户一旦手动选过币种就不再覆盖(避免改厂商时抹掉手动选择)。
  const curTouched = useRef(false);
  useEffect(() => {
    const info = vendors.find(v => v.provider === provider);
    if (!curTouched.current && info?.manualCurrency) {
      form.setFieldValue('currency', info.manualCurrency);
    }
  }, [provider, vendors, form]);

  // 每次打开按上下文预填(不保留上次残留)。
  useEffect(() => {
    if (!open) return;
    curTouched.current = false;
    const p = defaultProvider ?? options[0];
    const info = vendors.find(v => v.provider === p);
    form.resetFields();
    form.setFieldsValue({
      provider: p,
      modelName: defaultModelName ?? '',
      currency: info?.manualCurrency ?? 'CNY',
      inputPrice: 0,
      outputPrice: 0,
      cacheReadPrice: 0,
      cacheWritePrice: 0,
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, defaultProvider, defaultModelName]);

  const manual = useMutation({
    mutationFn: (v: ManualPriceDraft) => api.manualOfficialPrice(v),
    onSuccess: () => {
      message.success('官方参考价已录入');
      qc.invalidateQueries({ queryKey: ['official-prices'] });
      qc.invalidateQueries({ queryKey: ['models'] });
      onClose();
    },
    onError: (e) => message.error((e as Error)?.message || '录入失败'),
  });

  return (
    <Modal
      title="手工录入官方参考价"
      open={open}
      onCancel={onClose}
      onOk={() => form.submit()}
      confirmLoading={manual.isPending}
      okText="录入"
      cancelText="取消"
      width={560}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" onFinish={v => manual.mutate(v)} requiredMark={false}>
        <div className="gw-note" style={{ marginBottom: 14 }}>
          <span>请对照厂商官网计费页逐项填写,并粘贴官网页面地址作为来源 —— 手工录入同样需要可追溯核对。</span>
        </div>
        <Form.Item name="provider" label="供应商" rules={[{ required: true, message: '必填' }]}>
          <Select options={options.map(p => ({ value: p, label: p }))} />
        </Form.Item>
        <Form.Item
          name="modelName"
          label="模型名(渠道侧真实名)"
          rules={[{ required: true, whitespace: true, message: '必填' }]}
        >
          <Input className="gw-mono" placeholder="如 deepseek-flash" />
        </Form.Item>
        <Form.Item
          name="sourceUrl"
          label="来源 URL(官方计费页)"
          rules={[
            { required: true, whitespace: true, message: '必填:手工价也须可追溯' },
            { pattern: /^https?:\/\//, message: '须为 http(s) 地址' },
          ]}
        >
          <Input className="gw-mono" placeholder="https://bigmodel.cn/pricing" />
        </Form.Item>
        <Form.Item name="currency" label="币种" rules={[{ required: true, message: '必填' }]}>
          <Select onChange={() => { curTouched.current = true; }}
            options={[{ value: 'CNY', label: '人民币 CNY' }, { value: 'USD', label: '美元 USD' }]} />
        </Form.Item>
        <Space size={12} style={{ display: 'flex' }}>
          <Form.Item name="inputPrice" label="输入价(每百万 tokens)" rules={[{ required: true, message: '必填' }]} style={{ flex: 1 }}>
            <InputNumber min={0} precision={4} step={0.01} style={{ width: 160 }} />
          </Form.Item>
          <Form.Item name="outputPrice" label="输出价(每百万 tokens)" rules={[{ required: true, message: '必填' }]} style={{ flex: 1 }}>
            <InputNumber min={0} precision={4} step={0.01} style={{ width: 160 }} />
          </Form.Item>
        </Space>
        <Space size={12} style={{ display: 'flex' }}>
          <Form.Item name="cacheReadPrice" label="缓存命中读价" style={{ flex: 1 }}>
            <InputNumber min={0} precision={4} step={0.01} style={{ width: 160 }} />
          </Form.Item>
          <Form.Item name="cacheWritePrice" label="缓存写入价" tooltip="留 0 = 无依据(计费按输入价回落)" style={{ flex: 1 }}>
            <InputNumber min={0} precision={4} step={0.01} style={{ width: 160 }} />
          </Form.Item>
        </Space>
        <Form.Item name="nativeText" label="官网原文(可选)" tooltip="如限时折扣说明、档位描述,便于日后核对">
          <Input placeholder="例:限时 5 折 / 0<Token≤1M" />
        </Form.Item>
        <Form.Item name="note" label="备注(可选)">
          <Input.TextArea rows={2} />
        </Form.Item>
      </Form>
    </Modal>
  );
}
