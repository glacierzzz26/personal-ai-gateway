import { useState } from 'react';
import { App, Button, Card, Input, Select, Space, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery } from '@tanstack/react-query';
import PageHeader from '@/components/PageHeader';
import ProviderMark from '@/components/ProviderMark';
import StatusTag from '@/components/StatusTag';
import { api } from '@/services/api';
import { providers } from '@/services/mock/db';
import { fmt } from '@/utils/format';
import type { Channel, HealthStatus } from '@/types';

export default function Channels() {
  const { message } = App.useApp();
  const [kw, setKw] = useState('');
  const [provider, setProvider] = useState('');
  const [status, setStatus] = useState('');
  const [testingId, setTestingId] = useState<number | null>(null);

  const { data: channels = [], isLoading } = useQuery({ queryKey: ['channels'], queryFn: api.getChannels });

  const test = useMutation({
    mutationFn: (id: number) => api.testChannel(id),
    onSuccess: (r, id) => {
      setTestingId(null);
      const name = channels.find(c => c.id === id)?.name ?? '';
      if (r.ok) message.success(`${name} 探测成功 · ${fmt.ms(r.latencyMs)}`);
      else message.error(`${name} 探测失败`);
    },
    onError: () => { setTestingId(null); message.error('探测请求异常'); },
  });

  const list = channels.filter(c => {
    if (kw && !`${c.name}${c.baseUrl}`.toLowerCase().includes(kw.toLowerCase())) return false;
    if (provider && c.provider !== provider) return false;
    if (status && c.status !== status) return false;
    return true;
  });

  const columns: ColumnsType<Channel> = [
    {
      title: '名称', dataIndex: 'name',
      render: (v, r) => (
        <div>
          <div style={{ fontWeight: 500 }}>{v}</div>
          <div style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>{r.modelCount} 个模型</div>
        </div>
      ),
    },
    {
      title: '供应商', dataIndex: 'provider',
      render: v => (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <ProviderMark name={v} />{v}
        </span>
      ),
    },
    {
      title: 'Base URL', dataIndex: 'baseUrl',
      render: v => (
        <Typography.Text className="gw-mono" style={{ color: 'var(--gw-text-2)', maxWidth: 240 }} ellipsis>
          {v}
        </Typography.Text>
      ),
    },
    {
      title: '优先级', dataIndex: 'priority', align: 'right',
      render: v => <span className="gw-num">{v}</span>,
    },
    {
      title: '权重', dataIndex: 'weight', align: 'right',
      render: v => <span className="gw-num">{v}</span>,
    },
    {
      title: '成功率', dataIndex: 'successRate', align: 'right',
      render: v => <span className="gw-num">{fmt.pct(v, 2)}</span>,
    },
    {
      title: '延迟', dataIndex: 'latencyMs', align: 'right',
      render: (v, r) => <span className="gw-num">{r.status === 'down' ? '—' : fmt.ms(v)}</span>,
    },
    {
      title: '今日花费', dataIndex: 'todayCostUsd', align: 'right',
      render: v => <span className="gw-num">{fmt.usd(v)}</span>,
    },
    { title: '状态', dataIndex: 'status', render: v => <StatusTag status={v} /> },
    {
      title: '', align: 'right',
      render: (_, r) => (
        <Space size={4}>
          <Button
            size="small"
            loading={testingId === r.id}
            onClick={() => { setTestingId(r.id); test.mutate(r.id); }}
          >
            测试
          </Button>
          <Button size="small">编辑</Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="渠道管理"
        desc="一条渠道 = 一个上游 API 端点与凭据，管的是「怎么连上去」"
        extra={<Button type="primary">新建渠道</Button>}
      />

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginBottom: 16 }}>
        <Input.Search allowClear placeholder="搜索名称或地址" style={{ width: 220 }} value={kw} onChange={e => setKw(e.target.value)} />
        <Select
          style={{ width: 140 }} value={provider} onChange={setProvider}
          options={[{ value: '', label: '全部供应商' }, ...providers.map(p => ({ value: p, label: p }))]}
        />
        <Select
          style={{ width: 130 }} value={status} onChange={setStatus}
          options={[
            { value: '', label: '全部状态' },
            { value: 'healthy' satisfies HealthStatus, label: '健康' },
            { value: 'degraded' satisfies HealthStatus, label: '降级' },
            { value: 'down' satisfies HealthStatus, label: '不可用' },
          ]}
        />
      </div>

      <Card>
        <Table<Channel>
          rowKey="id"
          size="middle"
          loading={isLoading}
          dataSource={list}
          columns={columns}
          scroll={{ x: 1080 }}
          pagination={{ pageSize: 10, showSizeChanger: false }}
        />
      </Card>
    </div>
  );
}
