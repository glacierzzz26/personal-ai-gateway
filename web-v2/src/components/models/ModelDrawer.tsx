import { useMemo, useState } from 'react';
import { App, Button, Card, Col, Drawer, Row, Space, Switch, Table, Tabs, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import Chart from '@/components/Chart';
import ProviderMark from '@/components/ProviderMark';
import StatusTag from '@/components/StatusTag';
import { useSortableRows } from '@/hooks/useSortableRows';
import { useChartColors } from '@/hooks/useChartColors';
import { api } from '@/services/api';
import { CAP_LABEL, fmt } from '@/utils/format';
import type { EChartsOption } from 'echarts';
import type { ModelCatalogItem, ModelOffer } from '@/types';

interface Props {
  model: ModelCatalogItem | null;
  onClose: () => void;
}

export default function ModelDrawer({ model, onClose }: Props) {
  const { message } = App.useApp();
  const c = useChartColors();
  const qc = useQueryClient();
  const [tab, setTab] = useState('offers');

  const { data: models = [] } = useQuery({ queryKey: ['models'], queryFn: api.getModels });
  const current = model ? models.find(m => m.id === model.id) ?? model : null;

  const reorder = useMutation({
    mutationFn: (v: { modelId: number; from: number; insertAt: number }) =>
      api.reorderOffers(v.modelId, v.from, v.insertAt),
    onSuccess: (offers, v) => {
      qc.invalidateQueries({ queryKey: ['models'] });
      const target = v.insertAt > v.from ? v.insertAt - 1 : v.insertAt;
      message.success(`优先级已更新：${offers[target]?.channelName ?? ''} → 第 ${target + 1} 位`);
    },
    onError: () => message.error('调整失败，已回滚'),
  });

  const { onRow, handleProps } = useSortableRows((from, insertAt) => {
    if (!current) return;
    reorder.mutate({ modelId: current.id, from, insertAt });
  });

  const offers = current?.offers ?? [];
  const lowest = useMemo(() => {
    const on = offers.filter(o => o.enabled);
    return on.length ? Math.min(...on.map(o => o.inputPriceUsd)) : null;
  }, [offers]);

  const offerCols: ColumnsType<ModelOffer> = [
    {
      title: '', width: 32,
      render: (_, __, i) => (
        <span {...handleProps(i ?? 0)} aria-label="拖动调整优先级">⋮⋮</span>
      ),
    },
    { title: '渠道', dataIndex: 'channelName', render: v => <b style={{ fontWeight: 500 }}>{v}</b> },
    {
      title: '供应商', dataIndex: 'provider',
      render: v => (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <ProviderMark name={v} /> {v}
        </span>
      ),
    },
    {
      title: '输入价', dataIndex: 'inputPriceUsd', align: 'right',
      render: (v, r) => (
        <span className="gw-num" style={{ color: r.enabled && v === lowest ? 'var(--gw-primary)' : undefined }}>
          {fmt.price(v)}
        </span>
      ),
    },
    {
      title: '输出价', dataIndex: 'outputPriceUsd', align: 'right',
      render: v => <span className="gw-num">{fmt.price(v)}</span>,
    },
    {
      title: '延迟', dataIndex: 'latencyMs', align: 'right',
      render: v => <span className="gw-num">{v ? fmt.ms(v) : '—'}</span>,
    },
    {
      title: '成功率', dataIndex: 'successRate', align: 'right',
      render: v => <span className="gw-num">{fmt.pct(v, 2)}</span>,
    },
    {
      title: '状态', dataIndex: 'status',
      render: (_, r) => <StatusTag status={r.enabled ? r.status : 'disabled'} />,
    },
    {
      title: '优先级', dataIndex: 'priority', align: 'right',
      render: v => <span className="gw-num">{v}</span>,
    },
    {
      title: '启用', dataIndex: 'enabled', align: 'center',
      render: v => <Switch size="small" checked={v} />,
    },
    {
      title: '', align: 'right',
      render: () => (
        <Space size={4}>
          <Typography.Link>设为首选</Typography.Link>
          <Typography.Link>测试</Typography.Link>
        </Space>
      ),
    },
  ];

  const priceOption: EChartsOption = {
    grid: { left: 120, right: 70, top: 34, bottom: 8 },
    tooltip: {
      trigger: 'axis', axisPointer: { type: 'shadow' },
      backgroundColor: c.tooltipBg, borderColor: c.tooltipBorder,
      textStyle: { color: c.text, fontSize: 12 },
    },
    legend: {
      data: ['输入价', '输出价'], right: 0, top: 0,
      itemWidth: 8, itemHeight: 8, textStyle: { color: c.text, fontSize: 12 },
    },
    xAxis: {
      type: 'value', splitLine: { lineStyle: { color: c.line } },
      axisLabel: { color: c.text, fontSize: 11 },
    },
    yAxis: {
      type: 'category', data: offers.map(o => o.channelName),
      axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      axisLabel: { color: c.text, fontSize: 12 },
    },
    series: [
      {
        name: '输入价', type: 'bar', data: offers.map(o => o.inputPriceUsd),
        itemStyle: { color: c.primarySoft, borderRadius: 2 }, barGap: '20%', barCategoryGap: '45%',
      },
      {
        name: '输出价', type: 'bar', data: offers.map(o => o.outputPriceUsd),
        itemStyle: { color: c.primary, borderRadius: 2 },
      },
    ],
  };

  const usageOption: EChartsOption = {
    grid: { left: 44, right: 12, top: 14, bottom: 26 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: c.tooltipBg, borderColor: c.tooltipBorder,
      textStyle: { color: c.text, fontSize: 12 },
    },
    xAxis: {
      type: 'category',
      data: ['08-28', '08-29', '08-30', '08-31', '09-01', '09-02', '09-03'],
      axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      axisLabel: { color: c.text, fontSize: 11 },
    },
    yAxis: {
      type: 'value', splitLine: { lineStyle: { color: c.line } },
      axisLabel: { color: c.text, fontSize: 11 },
    },
    series: [{
      name: '调用量', type: 'bar',
      data: current ? current.offers.map((_, i) => Math.round(current.todayRequests * (.6 + ((i * 37) % 60) / 100))) : [],
      itemStyle: { color: c.primary, borderRadius: 3 }, barWidth: '45%',
    }],
  };

  return (
    <Drawer
      width={720}
      open={!!model}
      onClose={onClose}
      destroyOnClose
      title={
        current ? (
          <div>
            <span className="gw-mono" style={{ fontSize: 16, fontWeight: 600 }}>{current.name}</span>
            <span style={{ marginLeft: 12 }}>
              {current.capabilities.map(x => <Tag key={x}>{CAP_LABEL[x]}</Tag>)}
            </span>
            <span style={{ fontSize: 12, color: 'var(--gw-text-3)', marginLeft: 8 }}>
              {fmt.ctx(current.contextWindow)} 上下文
            </span>
          </div>
        ) : null
      }
      extra={<Switch checked={current?.enabled} />}
      footer={
        <Space style={{ float: 'right' }}>
          <Button onClick={onClose}>取消</Button>
          <Button type="primary">保存更改</Button>
        </Space>
      }
    >
      {current && (
        <Tabs
          activeKey={tab}
          onChange={setTab}
          items={[
            {
              key: 'offers',
              label: '供给源',
              children: (
                <>
                  <div
                    style={{
                      border: '1px solid var(--gw-border)', borderLeft: '2px solid var(--gw-primary)',
                      borderRadius: 6, padding: '10px 12px', fontSize: 13,
                      color: 'var(--gw-text-2)', background: 'var(--gw-fill)', marginBottom: 16,
                    }}
                  >
                    拖动左侧手柄可调整优先级，请求按此顺序尝试，上游失败自动降级到下一个可用供给源。路由规则的优先级高于此处。
                  </div>
                  <Table<ModelOffer>
                    rowKey="id"
                    size="middle"
                    dataSource={offers}
                    columns={offerCols}
                    pagination={false}
                    onRow={onRow}
                  />
                  <Button style={{ marginTop: 16 }}>+ 添加供给源</Button>
                </>
              ),
            },
            {
              key: 'price',
              label: '价格对比',
              children: (
                <>
                  <div style={{ fontSize: 12, color: 'var(--gw-text-3)', marginBottom: 12 }}>
                    单位：美元 / 1M tokens
                  </div>
                  <Chart option={priceOption} height={offers.length * 42 + 70} />
                  <Table<ModelOffer>
                    rowKey="id"
                    size="middle"
                    style={{ marginTop: 20 }}
                    dataSource={offers}
                    pagination={false}
                    columns={[
                      { title: '渠道', dataIndex: 'channelName' },
                      {
                        title: '上下文', align: 'right',
                        render: (_, r) => <span className="gw-num">{fmt.ctx(r.contextWindow)}</span>,
                      },
                      {
                        title: '限流', align: 'right',
                        render: (_, r) => <span className="gw-num">{r.rateLimitRpm} RPM</span>,
                      },
                      {
                        title: '输入价', align: 'right',
                        render: (_, r) => <span className="gw-num">{fmt.price(r.inputPriceUsd)}</span>,
                      },
                      {
                        title: '输出价', align: 'right',
                        render: (_, r) => <span className="gw-num">{fmt.price(r.outputPriceUsd)}</span>,
                      },
                      {
                        title: '备注', dataIndex: 'note',
                        render: v => <span style={{ color: 'var(--gw-text-3)' }}>{v || '—'}</span>,
                      },
                    ]}
                  />
                </>
              ),
            },
            {
              key: 'usage',
              label: '用量',
              children: (
                <>
                  <Row gutter={12} style={{ marginBottom: 16 }}>
                    {[
                      ['今日调用', fmt.k(current.todayRequests)],
                      ['今日花费', fmt.usd(current.todayRequests * .0009)],
                      ['平均延迟', fmt.ms(Math.round(offers.reduce((a, o) => a + o.latencyMs, 0) / (offers.length || 1)))],
                      ['成功率', fmt.pct(current.successRate)],
                    ].map(([label, value]) => (
                      <Col span={6} key={label}>
                        <Card>
                          <div style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>{label}</div>
                          <div className="gw-num" style={{ fontSize: 20, fontWeight: 600, marginTop: 6 }}>
                            {value}
                          </div>
                        </Card>
                      </Col>
                    ))}
                  </Row>
                  <Card title="近 7 日趋势" style={{ marginBottom: 16 }}>
                    <Chart option={usageOption} height={240} />
                  </Card>
                  <Card title="按渠道调用占比">
                    {offers.map(o => (
                      <div key={o.id} style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12, fontSize: 13 }}>
                        <span style={{ width: 130, color: 'var(--gw-text-2)' }}>{o.channelName}</span>
                        <div style={{ flex: 1, height: 4, borderRadius: 2, background: 'var(--gw-fill)', overflow: 'hidden' }}>
                          <div
                            style={{
                              height: '100%', borderRadius: 2, background: 'var(--gw-primary)',
                              width: `${100 / (offers.length || 1)}%`,
                            }}
                          />
                        </div>
                        <span className="gw-num" style={{ width: 70, textAlign: 'right' }}>
                          {(100 / (offers.length || 1)).toFixed(1)}%
                        </span>
                      </div>
                    ))}
                  </Card>
                </>
              ),
            },
          ]}
        />
      )}
    </Drawer>
  );
}
