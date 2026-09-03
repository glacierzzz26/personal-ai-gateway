import { useState } from 'react';
import { App, Button, Card, Descriptions, Drawer, Input, Select, Space, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useQuery } from '@tanstack/react-query';
import PageHeader from '@/components/PageHeader';
import StatusTag from '@/components/StatusTag';
import { api } from '@/services/api';
import { channels, models } from '@/services/mock/db';
import { fmt } from '@/utils/format';
import type { RequestLogItem } from '@/types';

export default function Logs() {
  const { message } = App.useApp();
  const [model, setModel] = useState('');
  const [channel, setChannel] = useState('');
  const [status, setStatus] = useState('');
  const [kw, setKw] = useState('');
  const [detail, setDetail] = useState<RequestLogItem | null>(null);

  const { data: logs = [], isLoading } = useQuery({ queryKey: ['logs'], queryFn: api.getLogs });

  const list = logs.filter(l => {
    if (model && l.model !== model) return false;
    if (channel && l.channelName !== channel) return false;
    if (status === 'error' && l.statusCode === 200) return false;
    if (status === 'ok' && l.statusCode !== 200) return false;
    if (kw && !`${l.id}${l.model}${l.tokenName}`.toLowerCase().includes(kw.toLowerCase())) return false;
    return true;
  });

  const columns: ColumnsType<RequestLogItem> = [
    {
      title: '时间', dataIndex: 'ts', width: 100,
      render: v => <span className="gw-mono">{String(v).slice(11)}</span>,
    },
    {
      title: '状态', dataIndex: 'statusCode', width: 90,
      render: v => <StatusTag status={v === 200 ? 'healthy' : 'down'} text={String(v)} />,
    },
    { title: '模型', dataIndex: 'model', render: v => <span className="gw-mono">{v}</span> },
    { title: '渠道', dataIndex: 'channelName', width: 130 },
    { title: '令牌', dataIndex: 'tokenName', width: 120 },
    {
      title: '输入', dataIndex: 'inTokens', align: 'right',
      render: v => <span className="gw-num">{fmt.k(v)}</span>,
    },
    {
      title: '输出', dataIndex: 'outTokens', align: 'right',
      render: v => <span className="gw-num">{fmt.k(v)}</span>,
    },
    {
      title: '首字', dataIndex: 'firstTokenMs', align: 'right',
      render: v => <span className="gw-num">{v ? fmt.ms(v) : '—'}</span>,
    },
    {
      title: '总耗时', dataIndex: 'totalMs', align: 'right',
      render: v => <span className="gw-num">{fmt.ms(v)}</span>,
    },
    {
      title: '花费', dataIndex: 'costUsd', align: 'right',
      render: v => <span className="gw-num">{fmt.usd(v, 4)}</span>,
    },
    {
      title: '', align: 'right', width: 80,
      render: (_, r) => <Button size="small" onClick={() => setDetail(r)}>详情</Button>,
    },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="请求日志"
        desc="逐条请求明细，点开可查看完整请求体与响应"
        extra={<Button onClick={() => message.info('原型：导出 CSV 待实现')}>导出 CSV</Button>}
      />

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center', marginBottom: 16 }}>
        <Select
          style={{ width: 130 }} defaultValue="24h"
          options={[{ value: '1h', label: '近 1 小时' }, { value: '24h', label: '近 24 小时' }, { value: '7d', label: '近 7 天' }]}
        />
        <Select
          style={{ width: 220 }} value={model} onChange={setModel} showSearch
          options={[{ value: '', label: '全部模型' }, ...models.map(m => ({ value: m.name, label: m.name }))]}
        />
        <Select
          style={{ width: 160 }} value={channel} onChange={setChannel}
          options={[{ value: '', label: '全部渠道' }, ...channels.map(c => ({ value: c.name, label: c.name }))]}
        />
        <Select
          style={{ width: 130 }} value={status} onChange={setStatus}
          options={[{ value: '', label: '全部状态码' }, { value: 'ok', label: '仅成功' }, { value: 'error', label: '仅错误' }]}
        />
        <Input.Search allowClear placeholder="关键词" style={{ width: 180 }} value={kw} onChange={e => setKw(e.target.value)} />
        <Button onClick={() => { setModel(''); setChannel(''); setStatus(''); setKw(''); }}>重置</Button>
      </div>

      <Card>
        <Table<RequestLogItem>
          rowKey="id"
          size="middle"
          loading={isLoading}
          dataSource={list}
          columns={columns}
          scroll={{ x: 1360 }}
          pagination={{ pageSize: 20, showSizeChanger: false, showTotal: t => `共 ${t} 条` }}
        />
      </Card>

      <Drawer
        width={680}
        open={!!detail}
        onClose={() => setDetail(null)}
        title={detail ? <span className="gw-mono">{detail.id}</span> : null}
        destroyOnClose
      >
        {detail && (
          <>
            {detail.error && (
              <div
                style={{
                  border: '1px solid var(--gw-border)', borderLeft: '2px solid #EF4444',
                  borderRadius: 6, padding: '10px 12px', fontSize: 13,
                  color: 'var(--gw-text-2)', background: 'var(--gw-fill)', marginBottom: 16,
                }}
              >
                {detail.error}
              </div>
            )}
            <Descriptions column={1} size="small" bordered styles={{ label: { width: 120 } }}>
              <Descriptions.Item label="请求 ID"><span className="gw-mono">{detail.id}</span></Descriptions.Item>
              <Descriptions.Item label="时间">{detail.ts}</Descriptions.Item>
              <Descriptions.Item label="模型"><span className="gw-mono">{detail.model}</span></Descriptions.Item>
              <Descriptions.Item label="渠道">{detail.channelName}</Descriptions.Item>
              <Descriptions.Item label="令牌">{detail.tokenName}</Descriptions.Item>
              <Descriptions.Item label="来源 IP">{detail.ip}</Descriptions.Item>
              <Descriptions.Item label="状态码">{detail.statusCode}</Descriptions.Item>
              <Descriptions.Item label="输入 Token"><span className="gw-num">{fmt.n(detail.inTokens)}</span></Descriptions.Item>
              <Descriptions.Item label="输出 Token"><span className="gw-num">{fmt.n(detail.outTokens)}</span></Descriptions.Item>
              <Descriptions.Item label="首字延迟">{detail.firstTokenMs ? fmt.ms(detail.firstTokenMs) : '—'}</Descriptions.Item>
              <Descriptions.Item label="总耗时">{fmt.ms(detail.totalMs)}</Descriptions.Item>
              <Descriptions.Item label="花费">{fmt.usd(detail.costUsd, 6)}</Descriptions.Item>
            </Descriptions>

            <div style={{ fontSize: 12, color: 'var(--gw-text-3)', margin: '20px 0 8px' }}>请求体预览</div>
            <pre className="gw-json">{`{
  "model": "${detail.model}",
  "messages": [
    { "role": "user", "content": "帮我把这段日志按渠道聚合，输出成表格…" }
  ],
  "stream": true,
  "temperature": 0.7
}`}</pre>
            <div style={{ marginTop: 16 }}>
              <Space>
                <Typography.Link onClick={() => message.info('原型：响应体预览待接入')}>查看响应体</Typography.Link>
                <Typography.Link onClick={() => message.info('原型：错误堆栈待接入')}>查看错误堆栈</Typography.Link>
              </Space>
            </div>
          </>
        )}
      </Drawer>
    </div>
  );
}
