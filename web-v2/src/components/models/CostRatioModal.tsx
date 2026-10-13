import { useEffect, useMemo, useState } from 'react';
import { Badge, Btn, IconBtn, Input, Modal, Note, Select } from '@/ui/primitives';
import { toast } from '@/ui/toast';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/services/api';
import type { Channel, ModelCostRatioRow, Provider } from '@/types';

interface Props {
  open: boolean;
  channel: Channel | null;
  onClose: () => void;
}

/** 编辑中的一行(与 CostRatioInput 的区别:ratio 可为空 = 未填,落库前拦截) */
interface Row {
  vendor: Provider;
  ratio: number | null;
  note: string;
}

/**
 * 成本系数编辑器(两段)。
 *
 * 成本 = 官方价 × 系数,计费**三级口径**:① 模型级(本表上段,订阅计划页自动派生 / 人工覆盖)
 * > ② 厂商级(本表下段,兜底)> ③ 1.0(不折扣)。
 *
 * 为什么加模型级:订阅计划页(CC GOAT / opencode Go)给**每个模型一个不同的月度额度**,
 * 真实系数 = 订阅价 ÷ 该模型额度,每模型一个值 —— 「一厂商同倍率」的旧口径只是兜底。
 *
 * 上段为**只读**(系数由订阅页抓取自动写入,或在此删除以退回厂商级);
 * 下段可编辑(手工维护厂商级兜底)。
 */
export default function CostRatioModal({ open, channel, onClose }: Props) {
  const qc = useQueryClient();
  const [rows, setRows] = useState<Row[]>([]);
  const [saving, setSaving] = useState(false);

  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['channel-cost-ratios', channel?.id],
    queryFn: () => api.channelCostRatios(channel!.id),
    enabled: open && !!channel,
    retry: 0,
  });

  // 模型级系数(订阅计划页自动派生 / 人工覆盖),只读展示 + 逐行删除。
  const {
    data: modelRows = [],
    isLoading: modelLoading,
    isError: modelError,
    error: modelErr,
    refetch: refetchModels,
  } = useQuery({
    queryKey: ['channel-model-cost-ratios', channel?.id],
    queryFn: () => api.channelModelCostRatios(channel!.id),
    enabled: open && !!channel,
    retry: 0,
  });

  // 厂商清单与「官方定价」页同源(可抓 + 仅手工录入都算,手工录的价同样要乘系数)。
  const { data: vendors = [] } = useQuery({
    queryKey: ['official-vendors'],
    queryFn: () => api.officialVendors(),
    retry: 0,
    staleTime: 300_000,
  });

  // 打开时用服务端快照重置本地编辑态 —— 关掉再开不该看到上次未保存的残留。
  useEffect(() => {
    if (open) setRows((data ?? []).map(r => ({ vendor: r.vendor, ratio: r.ratio, note: r.note ?? '' })));
  }, [open, data]);

  const usedVendors = useMemo(() => new Set(rows.map(r => r.vendor)), [rows]);
  const addable = vendors.filter(v => !usedVendors.has(v.provider));
  const invalid = rows.some(r => r.ratio == null || r.ratio <= 0);

  function addVendor(vendor: Provider) {
    // ratio 留空而非预填 1.0:1.0 是个有含义的数(不折扣),替人填上等于替人做了决定。
    setRows(rs => [...rs, { vendor, ratio: null, note: '' }]);
  }

  function patch(vendor: Provider, p: Partial<Row>) {
    setRows(rs => rs.map(r => (r.vendor === vendor ? { ...r, ...p } : r)));
  }

  async function handleSave() {
    if (!channel) return;
    const payload = rows.map(r => ({
      vendor: r.vendor,
      ratio: r.ratio as number,
      note: r.note.trim(),
    }));
    setSaving(true);
    try {
      await api.replaceChannelCostRatios(channel.id, payload);
      toast.ok(payload.length ? `已保存 ${payload.length} 条厂商级系数` : '已清空厂商级系数(全部厂商按 1.0 计)');
      // 成本派生自系数,系数一改,模型毛利列/用户面报价都要重取。
      qc.invalidateQueries({ queryKey: ['channel-cost-ratios', channel.id] });
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['official-prices'] });
      onClose();
    } catch (e) {
      toast.err(`保存失败:${(e as Error)?.message || '未知错误'}`);
    } finally {
      setSaving(false);
    }
  }

  async function handleDeleteModel(r: ModelCostRatioRow) {
    if (!channel) return;
    try {
      await api.deleteChannelModelCostRatio(channel.id, r.modelName);
      toast.ok(`已删除 ${r.modelName} 的模型级系数(退回厂商级兜底)`);
      qc.invalidateQueries({ queryKey: ['channel-model-cost-ratios', channel.id] });
      qc.invalidateQueries({ queryKey: ['models'] });
      void refetchModels();
    } catch (e) {
      toast.err(`删除失败:${(e as Error)?.message || '未知错误'}`);
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={channel ? `成本系数 · ${channel.name}` : '成本系数'}
      width={760}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn variant="primary" disabled={invalid} loading={saving} onClick={handleSave}>保存厂商级系数</Btn>
        </>
      }
    >
      <Note>
        <b>成本 = 官方价 × 系数(查找顺序:模型级 → 厂商级 → 1.0)</b>
        <div style={{ marginTop: 4, fontSize: 12.5 }}>
          <b>模型级</b>由订阅计划页(CC GOAT / opencode Go)自动派生 —— 系数 = 订阅价 ÷ 该模型月度额度,
          每模型一个值;可在下方逐行删除以退回厂商级。<b>厂商级</b>是兜底,官方价一变、系数一改即时重算。
        </div>
      </Note>

      <div style={{ fontWeight: 600, margin: '4px 0 8px' }}>模型级(订阅计划自动派生)</div>
      {modelError && (
        <Note tone="warn" style={{ marginBottom: 12 }}>
          <b>模型级系数加载失败</b>
          <div style={{ marginTop: 4 }}>{(modelErr as Error)?.message}</div>
          <Btn size="sm" style={{ marginTop: 6 }} onClick={() => void refetchModels()}>重试</Btn>
        </Note>
      )}
      <div className="table-wrap">
        <table className="tb">
          <thead>
            <tr>
              <th>模型</th>
              <th>成本系数</th>
              <th>来源</th>
              <th>备注</th>
              <th className="right">操作</th>
            </tr>
          </thead>
          <tbody>
            {modelRows.length === 0 && !modelLoading ? (
              <tr><td colSpan={5} className="cell-sub" style={{ textAlign: 'center', padding: 18 }}>
                尚无模型级系数 —— 点「官方定价」页的「从订阅计划抓取」自动派生
              </td></tr>
            ) : modelRows.map(r => (
              <tr key={r.modelName}>
                <td className="mono strong">{r.modelName}</td>
                <td className="num">{Number(r.ratio).toFixed(4)}</td>
                <td>{r.source === 'manual' ? <Badge tone="brand">人工</Badge> : <Badge tone="green">自动</Badge>}</td>
                <td className="cell-sub">{r.note || '—'}</td>
                <td className="right">
                  <IconBtn n="trash" title={`删除 ${r.modelName} 的模型级系数`} onClick={() => void handleDeleteModel(r)} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div style={{ fontWeight: 600, margin: '16px 0 8px' }}>厂商级(兜底层)</div>
      {isError && (
        <Note tone="warn" style={{ marginBottom: 12 }}>
          <b>厂商级系数加载失败</b>
          <div style={{ marginTop: 4 }}>{(error as Error)?.message}</div>
          <Btn size="sm" style={{ marginTop: 6 }} onClick={() => void refetch()}>重试</Btn>
        </Note>
      )}
      <div className="table-wrap">
        <table className="tb">
          <thead>
            <tr>
              <th>厂商</th>
              <th>成本系数</th>
              <th>备注</th>
              <th className="right">操作</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && !isLoading ? (
              <tr><td colSpan={4} className="cell-sub" style={{ textAlign: 'center', padding: 18 }}>
                未配置厂商级兜底 —— 模型级缺行时按 1.0 计(成本 = 官方挂牌价)
              </td></tr>
            ) : rows.map(r => (
              <tr key={r.vendor}>
                <td className="mono strong">{r.vendor}</td>
                <td>
                  <Input
                    type="number" step="0.01" min="0.0001" style={{ width: 110 }}
                    value={r.ratio ?? ''} placeholder="必填"
                    onChange={e => patch(r.vendor, { ratio: e.target.value === '' ? null : Number(e.target.value) })}
                  />
                </td>
                <td>
                  <Input value={r.note} placeholder="如:$10 买 $60 额度" onChange={e => patch(r.vendor, { note: e.target.value })} />
                </td>
                <td className="right">
                  <IconBtn n="trash" title={`删除 ${r.vendor} 的系数`} onClick={() => setRows(rs => rs.filter(x => x.vendor !== r.vendor))} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div style={{ marginTop: 12 }}>
        <Select
          value=""
          disabled={addable.length === 0}
          onChange={e => { if (e.target.value) addVendor(e.target.value as Provider); }}
        >
          <option value="">添加厂商</option>
          {addable.map(v => <option key={v.provider} value={v.provider}>{v.provider}</option>)}
        </Select>
        <span className="cell-sub" style={{ marginLeft: 10, fontSize: 12.5 }}>
          系数 0 不接受(要表达免费请删除该行)
        </span>
      </div>
    </Modal>
  );
}
