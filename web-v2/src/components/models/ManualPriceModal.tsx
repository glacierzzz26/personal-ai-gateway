import { useEffect, useMemo, useRef, useState } from 'react';
import { Btn, Field, Input, Modal, Note, Select } from '@/ui/primitives';
import { toast } from '@/ui/toast';
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
  const qc = useQueryClient();
  const [f, setF] = useState<ManualPriceDraft>({
    provider: '' as Provider,
    modelName: '',
    sourceUrl: '',
    currency: 'CNY',
    inputPrice: 0,
    outputPrice: 0,
    cacheReadPrice: 0,
    cacheWritePrice: 0,
    nativeText: '',
    note: '',
  });

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

  const provider = f.provider;
  // 默认原币随厂商:Anthropic/OpenAI/Azure 官网标美元,国内厂商标人民币。
  // 用户一旦手动选过币种就不再覆盖(避免改厂商时抹掉手动选择)。
  const curTouched = useRef(false);
  useEffect(() => {
    const info = vendors.find(v => v.provider === provider);
    if (!curTouched.current && info?.manualCurrency) {
      setF(p => ({ ...p, currency: info.manualCurrency as 'CNY' | 'USD' }));
    }
  }, [provider, vendors]);

  // 每次打开按上下文预填(不保留上次残留)。
  useEffect(() => {
    if (!open) return;
    curTouched.current = false;
    const p = defaultProvider ?? options[0];
    const info = vendors.find(v => v.provider === p);
    setF({
      provider: p,
      modelName: defaultModelName ?? '',
      sourceUrl: '',
      currency: info?.manualCurrency ?? 'CNY',
      inputPrice: 0,
      outputPrice: 0,
      cacheReadPrice: 0,
      cacheWritePrice: 0,
      nativeText: '',
      note: '',
    });
  }, [open, defaultProvider, defaultModelName, options, vendors]);

  const manual = useMutation({
    mutationFn: (v: ManualPriceDraft) => api.manualOfficialPrice(v),
    onSuccess: () => {
      toast.ok('官方参考价已录入');
      qc.invalidateQueries({ queryKey: ['official-prices'] });
      qc.invalidateQueries({ queryKey: ['models'] });
      onClose();
    },
    onError: (e) => toast.err((e as Error)?.message || '录入失败'),
  });

  const submit = () => {
    if (!f.provider) { toast.err('请选择供应商'); return; }
    if (!f.modelName.trim()) { toast.err('请填写模型名(渠道侧真实名)'); return; }
    if (!/^https?:\/\//.test(f.sourceUrl)) { toast.err('来源 URL 须为 http(s) 地址'); return; }
    manual.mutate({
      ...f,
      modelName: f.modelName.trim(),
      nativeText: f.nativeText || undefined,
      note: f.note || undefined,
    });
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="手工录入官方参考价"
      width={560}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn variant="primary" loading={manual.isPending} onClick={submit}>录入</Btn>
        </>
      }
    >
      <Note>请对照厂商官网计费页逐项填写,并粘贴官网页面地址作为来源 —— 手工录入同样需要可追溯核对。</Note>

      <div style={{ marginTop: 14 }}>
        <Field label="供应商">
          <Select value={f.provider} onChange={e => setF(p => ({ ...p, provider: e.target.value as Provider }))}>
            <option value="">请选择</option>
            {options.map(p => <option key={p} value={p}>{p}</option>)}
          </Select>
        </Field>

        <Field label="模型名(渠道侧真实名)">
          <Input className="mono" value={f.modelName} placeholder="如 deepseek-flash"
            onChange={e => setF(p => ({ ...p, modelName: e.target.value }))} />
        </Field>

        <Field label="来源 URL(官方计费页)">
          <Input className="mono" value={f.sourceUrl} placeholder="https://bigmodel.cn/pricing"
            onChange={e => setF(p => ({ ...p, sourceUrl: e.target.value }))} />
        </Field>

        <Field label="币种">
          <Select value={f.currency} onChange={e => { curTouched.current = true; setF(p => ({ ...p, currency: e.target.value as 'CNY' | 'USD' })); }}>
            <option value="CNY">人民币 CNY</option>
            <option value="USD">美元 USD</option>
          </Select>
        </Field>

        <div className="form-grid">
          <Field label="输入价(每百万 tokens)">
            <Input type="number" step="0.01" min="0" value={String(f.inputPrice)}
              onChange={e => setF(p => ({ ...p, inputPrice: Number(e.target.value) || 0 }))} />
          </Field>
          <Field label="输出价(每百万 tokens)">
            <Input type="number" step="0.01" min="0" value={String(f.outputPrice)}
              onChange={e => setF(p => ({ ...p, outputPrice: Number(e.target.value) || 0 }))} />
          </Field>
        </div>

        <div className="form-grid">
          <Field label="缓存命中读价">
            <Input type="number" step="0.01" min="0" value={String(f.cacheReadPrice)}
              onChange={e => setF(p => ({ ...p, cacheReadPrice: Number(e.target.value) || 0 }))} />
          </Field>
          <Field label="缓存写入价">
            <Input type="number" step="0.01" min="0" value={String(f.cacheWritePrice)}
              onChange={e => setF(p => ({ ...p, cacheWritePrice: Number(e.target.value) || 0 }))} />
          </Field>
        </div>

        <Field label="官网原文(可选)" hint="如限时折扣说明、档位描述,便于日后核对">
          <Input value={f.nativeText ?? ''} placeholder="例:限时 5 折 / 0<Token≤1M"
            onChange={e => setF(p => ({ ...p, nativeText: e.target.value }))} />
        </Field>

        <Field label="备注(可选)">
          <Input value={f.note ?? ''} onChange={e => setF(p => ({ ...p, note: e.target.value }))} />
        </Field>
      </div>
    </Modal>
  );
}
