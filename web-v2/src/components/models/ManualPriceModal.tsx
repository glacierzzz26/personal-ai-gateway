import { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Field, HStack, Input, Select } from '@/ui/primitives';
import { Modal } from '@/ui/overlay';
import { message } from '@/ui/toast';
import { api } from '@/services/api';
import type { ManualPriceDraft, PriceCurrency, Provider } from '@/types';

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

/** 表单内部状态:数字字段留字符串,提交时再解析(空串才好校验)。 */
interface FormState {
  provider: Provider | '';
  modelName: string;
  sourceUrl: string;
  currency: PriceCurrency;
  inputPrice: string;
  outputPrice: string;
  cacheReadPrice: string;
  cacheWritePrice: string;
  nativeText: string;
  note: string;
}

const EMPTY: FormState = {
  provider: '',
  modelName: '',
  sourceUrl: '',
  currency: 'CNY',
  inputPrice: '0',
  outputPrice: '0',
  cacheReadPrice: '0',
  cacheWritePrice: '0',
  nativeText: '',
  note: '',
};

/**
 * 手工录入官方参考价(页面动态渲染、无法稳定抓取的厂商)。
 * 官方价与手工报价分表:此处只写 official_prices,不动任何 offer。
 */
export default function ManualPriceModal({
  open, defaultProvider, defaultModelName, providerOptions, onClose,
}: Props) {
  const qc = useQueryClient();
  const [f, setF] = useState<FormState>(EMPTY);
  const [errs, setErrs] = useState<Record<string, string>>({});

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

  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => {
    setF((s) => ({ ...s, [k]: v }));
    setErrs((e) => (e[k as string] ? { ...e, [k as string]: '' } : e));
  };

  // 默认原币随厂商:Anthropic/OpenAI/Azure 官网标美元,国内厂商标人民币。
  // 用户一旦手动选过币种就不再覆盖(避免改厂商时抹掉手动选择)。
  const curTouched = useRef(false);
  useEffect(() => {
    const info = vendors.find(v => v.provider === f.provider);
    if (!curTouched.current && info?.manualCurrency) {
      setF((s) => ({ ...s, currency: info.manualCurrency as PriceCurrency }));
    }
  }, [f.provider, vendors]);

  // 每次打开按上下文预填(不保留上次残留)。
  useEffect(() => {
    if (!open) return;
    curTouched.current = false;
    const p = defaultProvider ?? options[0] ?? '';
    const info = vendors.find(v => v.provider === p);
    setF({
      ...EMPTY,
      provider: p,
      modelName: defaultModelName ?? '',
      currency: (info?.manualCurrency as PriceCurrency) ?? 'CNY',
    });
    setErrs({});
    // 仅在打开 / 上下文变化时重置一次;options/vendors 由查询异步补齐后不重复覆盖用户输入
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

  const validate = (): Record<string, string> => {
    const e: Record<string, string> = {};
    if (!f.provider) e.provider = '必填';
    if (!f.modelName.trim()) e.modelName = '必填';
    if (!f.sourceUrl.trim()) e.sourceUrl = '必填:手工价也须可追溯';
    else if (!/^https?:\/\//.test(f.sourceUrl.trim())) e.sourceUrl = '须为 http(s) 地址';
    if (!Number.isFinite(Number(f.inputPrice)) || Number(f.inputPrice) < 0) e.inputPrice = '必填';
    if (!Number.isFinite(Number(f.outputPrice)) || Number(f.outputPrice) < 0) e.outputPrice = '必填';
    return e;
  };

  const submit = () => {
    const e = validate();
    if (Object.keys(e).length) {
      setErrs(e);
      return;
    }
    const v: ManualPriceDraft = {
      provider: f.provider as Provider,
      modelName: f.modelName.trim(),
      sourceUrl: f.sourceUrl.trim(),
      currency: f.currency,
      inputPrice: Number(f.inputPrice),
      outputPrice: Number(f.outputPrice),
      cacheReadPrice: Number(f.cacheReadPrice),
      cacheWritePrice: Number(f.cacheWritePrice),
      nativeText: f.nativeText.trim() || undefined,
      note: f.note.trim() || undefined,
    };
    manual.mutate(v);
  };

  if (!open) return null;

  return (
    <div className="overlay" style={{ zIndex: 70 }}>
      <Modal
        title="手工录入官方参考价"
        onClose={onClose}
        width={560}
        footer={
          <>
            <Button onClick={onClose}>取消</Button>
            <Button variant="primary" loading={manual.isPending} onClick={submit}>录入</Button>
          </>
        }
      >
        <div className="note note-info" style={{ marginBottom: 14 }}>
          <span>请对照厂商官网计费页逐项填写,并粘贴官网页面地址作为来源 —— 手工录入同样需要可追溯核对。</span>
        </div>

        <div className="stack">
          <Field label="供应商" error={errs.provider}>
            <Select
              value={f.provider}
              onChange={(e) => set('provider', e.target.value as Provider)}
              options={options.map(p => ({ value: p, label: p }))}
            />
          </Field>

          <Field label="模型名(渠道侧真实名)" error={errs.modelName}>
            <Input
              className="mono"
              value={f.modelName}
              placeholder="如 deepseek-flash"
              onChange={(e) => set('modelName', e.target.value)}
              invalid={!!errs.modelName}
            />
          </Field>

          <Field label="来源 URL(官方计费页)" error={errs.sourceUrl}>
            <Input
              className="mono"
              value={f.sourceUrl}
              placeholder="https://bigmodel.cn/pricing"
              onChange={(e) => set('sourceUrl', e.target.value)}
              invalid={!!errs.sourceUrl}
            />
          </Field>

          <Field label="币种">
            <Select
              value={f.currency}
              onChange={(e) => { curTouched.current = true; set('currency', e.target.value as PriceCurrency); }}
              options={[{ value: 'CNY', label: '人民币 CNY' }, { value: 'USD', label: '美元 USD' }]}
            />
          </Field>

          <HStack gap={12} style={{ alignItems: 'flex-start' }}>
            <Field label="输入价(每百万 tokens)" error={errs.inputPrice} className="grow">
              <Input type="number" min={0} step={0.01} value={f.inputPrice} onChange={(e) => set('inputPrice', e.target.value)} invalid={!!errs.inputPrice} />
            </Field>
            <Field label="输出价(每百万 tokens)" error={errs.outputPrice} className="grow">
              <Input type="number" min={0} step={0.01} value={f.outputPrice} onChange={(e) => set('outputPrice', e.target.value)} invalid={!!errs.outputPrice} />
            </Field>
          </HStack>

          <HStack gap={12} style={{ alignItems: 'flex-start' }}>
            <Field label="缓存命中读价" className="grow">
              <Input type="number" min={0} step={0.01} value={f.cacheReadPrice} onChange={(e) => set('cacheReadPrice', e.target.value)} />
            </Field>
            <Field label="缓存写入价" hint="留 0 = 无依据(计费按输入价回落)" className="grow">
              <Input type="number" min={0} step={0.01} value={f.cacheWritePrice} onChange={(e) => set('cacheWritePrice', e.target.value)} />
            </Field>
          </HStack>

          <Field label="官网原文(可选)" hint="如限时折扣说明、档位描述,便于日后核对">
            <Input value={f.nativeText} placeholder="例:限时 5 折 / 0<Token≤1M" onChange={(e) => set('nativeText', e.target.value)} />
          </Field>

          <Field label="备注(可选)">
            <textarea className="input" rows={2} value={f.note} onChange={(e) => set('note', e.target.value)} />
          </Field>
        </div>
      </Modal>
    </div>
  );
}
