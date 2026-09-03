import { useState } from 'react';
import { App, Button, Card, Modal, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery } from '@tanstack/react-query';
import PageHeader from '@/components/PageHeader';
import StatusTag from '@/components/StatusTag';
import { api } from '@/services/api';
import { fmt } from '@/utils/format';
import type { GatewayToken } from '@/types';

export default function Tokens() {
  const { message } = App.useApp();
  const [open, setOpen] = useState(false);
  const [newKey, setNewKey] = useState('');

  const { data: tokens = [], isLoading } = useQuery({ queryKey: ['tokens'], queryFn: api.getTokens });

  const create = useMutation({
    mutationFn: () => api.createToken(),
    onSuccess: r => { setNewKey(r.key); setOpen(true); },
  });

  const copy = (text: string) => {
    navigator.clipboard?.writeText(text).then(
      () => message.success('已复制'),
      () => message.warning('复制失败，请手动选择'),
    );
  };

  const columns: ColumnsType<GatewayToken> = [
    { title: '名称', dataIndex: 'name', render: v => <b style={{ fontWeight: 500 }}>{v}</b> },
    {
      title: 'Key', dataIndex: 'keyMasked', width: 190,
      render: v => (
        <span>
          <span className="gw-mono">{v}</span>
          <Typography.Link style={{ marginLeft: 8 }} onClick={() => copy(v)}>复制</Typography.Link>
        </span>
      ),
    },
    {
      title: '可用模型', dataIndex: 'allowedModels', width: 240,
      render: v => (v[0] === '*'
        ? <Tag>不限</Tag>
        : v.map((m: string) => <Tag key={m}>{m}</Tag>)),
    },
    {
      title: '额度使用', key: 'quota', width: 220,
      render: (_, r) => {
        const rate = r.quotaUsd ? r.usedUsd / r.quotaUsd : 0;
        const color = rate > .9 ? '#EF4444' : rate > .8 ? '#F59E0B' : 'var(--gw-primary)';
        return (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <div style={{ flex: 1, height: 4, borderRadius: 2, background: 'var(--gw-fill)', overflow: 'hidden' }}>
              <div style={{ height: '100%', width: `${Math.min(rate * 100, 100)}%`, background: color, borderRadius: 2 }} />
            </div>
            <span className="gw-num" style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
              {fmt.usd(r.usedUsd)} / {r.quotaUsd}
            </span>
          </div>
        );
      },
    },
    {
      title: 'RPM', dataIndex: 'rpmLimit', align: 'right',
      render: v => <span className="gw-num">{v}</span>,
    },
    {
      title: '过期时间', dataIndex: 'expiresAt', width: 120,
      render: v => (
        <span style={{ color: v && String(v) < '2026-09-10' ? '#F59E0B' : undefined }}>
          {v || '永不过期'}
        </span>
      ),
    },
    {
      title: '最后使用', dataIndex: 'lastUsedAt', width: 150,
      render: v => <span style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>{v}</span>,
    },
    {
      title: '状态', dataIndex: 'status',
      render: v => <StatusTag status={v === 'active' ? 'healthy' : v === 'expired' ? 'down' : 'disabled'} />,
    },
    {
      title: '', align: 'right', width: 120,
      render: () => (
        <Space size={4}>
          <Button size="small">编辑</Button>
          <Button size="small" danger>删除</Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="访问令牌"
        desc="对外分发的网关 Key，可独立设额度、限流与过期时间"
        extra={<Button type="primary" loading={create.isPending} onClick={() => create.mutate()}>新建令牌</Button>}
      />

      <Card>
        <Table<GatewayToken>
          rowKey="id"
          size="middle"
          loading={isLoading}
          dataSource={tokens}
          columns={columns}
          scroll={{ x: 1280 }}
          pagination={false}
        />
      </Card>

      <Modal
        open={open}
        title="令牌已创建"
        onCancel={() => setOpen(false)}
        footer={<Button type="primary" onClick={() => setOpen(false)}>我已保存</Button>}
      >
        <div
          style={{
            border: '1px solid var(--gw-border)', borderLeft: '2px solid #F59E0B',
            borderRadius: 6, padding: '10px 12px', fontSize: 13,
            color: 'var(--gw-text-2)', background: 'var(--gw-fill)', marginBottom: 16,
          }}
        >
          这是唯一一次显示完整 Key 的机会，关闭后将无法再次查看。
        </div>
        <div style={{ fontSize: 13, color: 'var(--gw-text-2)', marginBottom: 6 }}>完整 Key</div>
        <div
          style={{
            display: 'flex', alignItems: 'center', gap: 8, wordBreak: 'break-all',
            fontFamily: 'ui-monospace, Menlo, Consolas, monospace', fontSize: 13,
            border: '1px solid var(--gw-border)', borderRadius: 8, padding: 12,
            background: 'var(--gw-fill)',
          }}
        >
          {newKey}
          <Button size="small" style={{ marginLeft: 'auto', flex: '0 0 auto' }} onClick={() => copy(newKey)}>
            复制
          </Button>
        </div>
      </Modal>
    </div>
  );
}
