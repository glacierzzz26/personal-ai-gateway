import { useEffect, useMemo, useState } from 'react';
import { Alert, App, Button, Input, InputNumber, Modal, Select, Space, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api, suggestedRatio } from '@/services/api';
import type { Channel, Provider } from '@/types';

interface Props {
  open: boolean;
  channel: Channel | null;
  onClose: () => void;
}

/** 编辑中的一行(与 CostRatioRow 的区别:ratio 可为空 = 未填,落库前拦截) */
interface Row {
  vendor: Provider;
  ratio: number | null;
  note: string;
  /** 系数是按渠道类型预填的建议值,人还没核对过 —— 存前显式提醒 */
  suggested?: boolean;
}

/**
 * 「渠道 × 厂商」成本系数编辑器。
 *
 * 成本 = 该厂商官方价 × 系数。这里配的是**站主实付上游的折扣率**,不是售价:
 * credit 型套餐($10 买 $60 额度)对所有模型同倍率,所以按厂商配而非按模型配。
 *
 * 缺行 = 1.0(不折扣)。1.0 是「不折扣」这个明确含义,不是编造的数字 ——
 * 但它会让毛利看起来偏低,所以计费侧对未设系数的渠道会带一条 warn 提示。
 */
export default function CostRatioModal({ open, channel, onClose }: Props) {
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [rows, setRows] = useState<Row[]>([]);
  const [saving, setSaving] = useState(false);

  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['channel-cost-ratios', channel?.id],
    queryFn: () => api.channelCostRatios(channel!.id),
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
  // 只依赖 open:保存后 data 变化时弹窗已在关闭流程里,不会覆盖到人的输入。
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (open) setRows((data ?? []).map(r => ({ vendor: r.vendor, ratio: r.ratio, note: r.note ?? '' })));
  }, [open]);

  const usedVendors = useMemo(() => new Set(rows.map(r => r.vendor)), [rows]);
  const addable = vendors.filter(v => !usedVendors.has(v.provider));
  const hasSuggested = rows.some(r => r.suggested);
  const invalid = rows.some(r => r.ratio == null || r.ratio <= 0);

  function addVendor(vendor: Provider) {
    // 建议值只预填、不静默落库:$10 买 $60 是商业事实,该由人确认后写。
    const sug = suggestedRatio(channel?.channelType, vendor);
    // ratio 留空而非预填 1.0:1.0 是个有含义的数(不折扣),替人填上等于替人做了决定。
    setRows(rs => [...rs, { vendor, ratio: sug?.ratio ?? null, note: sug?.note ?? '', suggested: !!sug }]);
  }

  function patch(vendor: Provider, p: Partial<Row>) {
    // 人一动手就不再算「未核对的建议值」。
    setRows(rs => rs.map(r => (r.vendor === vendor ? { ...r, ...p, suggested: false } : r)));
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
      message.success(payload.length ? `已保存 ${payload.length} 条成本系数` : '已清空成本系数(全部厂商按 1.0 计)');
      // 成本派生自系数,系数一改,模型毛利列/用户面报价都要重取。
      qc.invalidateQueries({ queryKey: ['channel-cost-ratios', channel.id] });
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['official-prices'] });
      onClose();
    } catch (e) {
      message.error(`保存失败:${(e as Error)?.message || '未知错误'}`);
    } finally {
      setSaving(false);
    }
  }

  const columns: ColumnsType<Row> = [
    {
      title: '厂商', dataIndex: 'vendor', width: 160,
      render: (v: Provider, r) => (
        <Space size={6}>
          <span>{v}</span>
          {r.suggested && <Tag color="orange" style={{ marginInlineEnd: 0 }}>建议值待确认</Tag>}
        </Space>
      ),
    },
    {
      title: '成本系数', dataIndex: 'ratio', width: 150,
      render: (_, r) => (
        <InputNumber
          min={0.0001}
          step={0.01}
          precision={4}
          style={{ width: 110 }}
          value={r.ratio ?? undefined}
          placeholder="必填"
          onChange={v => patch(r.vendor, { ratio: v == null ? null : Number(v) })}
        />
      ),
    },
    {
      title: '备注', dataIndex: 'note',
      render: (_, r) => (
        <Input
          value={r.note}
          placeholder="如:$10 买 $60 额度"
          onChange={e => patch(r.vendor, { note: e.target.value })}
        />
      ),
    },
    {
      title: '', key: 'op', width: 44, align: 'right',
      render: (_, r) => (
        <Button
          type="text" size="small" danger
          icon={<DeleteOutlined />}
          aria-label={`删除 ${r.vendor} 的系数`}
          onClick={() => setRows(rs => rs.filter(x => x.vendor !== r.vendor))}
        />
      ),
    },
  ];

  return (
    <Modal
      title={channel ? `成本系数 · ${channel.name}` : '成本系数'}
      open={open}
      onCancel={onClose}
      onOk={handleSave}
      confirmLoading={saving}
      okText="保存"
      cancelText="取消"
      okButtonProps={{ disabled: invalid }}
      width={680}
      destroyOnHidden
    >
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message="成本 = 厂商官方价 × 本系数"
        description={
          <span style={{ fontSize: 12.5 }}>
            这里填的是<b>你实付上游的折扣率</b>,不是售价。官方价一变、系数一改,全渠道成本即时重算。
            未配的厂商按 <b>1.0</b> 计(即成本 = 官方挂牌价),会明显高估成本,建议逐个配齐。
          </span>
        }
      />

      {hasSuggested && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message="有按渠道类型预填的建议系数,请核对后保存 —— 不会自动落库"
        />
      )}

      {isError && (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 12 }}
          message="系数加载失败"
          description={(error as Error)?.message}
          action={<Button size="small" onClick={() => void refetch()}>重试</Button>}
        />
      )}

      <Table
        rowKey="vendor"
        size="small"
        columns={columns}
        dataSource={rows}
        loading={isLoading}
        pagination={false}
        locale={{ emptyText: '尚未配置 —— 该渠道全部厂商按 1.0 计(成本 = 官方挂牌价)' }}
      />

      <div style={{ marginTop: 12 }}>
        <Select
          style={{ width: 240 }}
          placeholder="添加厂商"
          value={null}
          suffixIcon={<PlusOutlined />}
          disabled={addable.length === 0}
          options={addable.map(v => ({ value: v.provider, label: v.provider }))}
          onSelect={(v: Provider | null) => v && addVendor(v)}
        />
        <span className="gw-note" style={{ marginLeft: 10, fontSize: 12.5 }}>
          系数 0 不接受(要表达免费请删除该行)
        </span>
      </div>
    </Modal>
  );
}
