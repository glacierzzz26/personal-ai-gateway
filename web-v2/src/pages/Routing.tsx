import { App, Card, Switch, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import PageHeader from '@/components/PageHeader';
import { useSortableRows } from '@/hooks/useSortableRows';
import { api } from '@/services/api';
import { channels } from '@/services/mock/db';
import { fmt } from '@/utils/format';
import type { RouteRule } from '@/types';

const MODE_TEXT: Record<string, string> = { prefix: '前缀', wildcard: '通配', regex: '正则' };
const STRATEGY_TEXT: Record<string, string> = { priority: '优先级', weight: '权重', latency: '最低延迟' };

export default function Routing() {
  const { message } = App.useApp();
  const qc = useQueryClient();
  const { data: rules = [], isLoading } = useQuery({ queryKey: ['rules'], queryFn: api.getRules });

  const reorder = useMutation({
    mutationFn: (v: { from: number; insertAt: number }) => api.reorderRules(v.from, v.insertAt),
    onSuccess: (_, v) => {
      qc.invalidateQueries({ queryKey: ['rules'] });
      const target = v.insertAt > v.from ? v.insertAt - 1 : v.insertAt;
      message.success(`顺序已更新 → 第 ${target + 1} 位`);
    },
    onError: () => message.error('调整失败，已回滚'),
  });

  const { onRow, handleProps } = useSortableRows((from, insertAt) => reorder.mutate({ from, insertAt }));

  const columns: ColumnsType<RouteRule> = [
    {
      title: '', width: 32,
      render: (_, __, i) => <span {...handleProps(i ?? 0)} aria-label="拖动调整顺序">⋮⋮</span>,
    },
    {
      title: '规则名', dataIndex: 'name',
      width: 200,
      render: (v, r) => (
        <div>
          <div style={{ fontWeight: 500 }}>{v}</div>
          <div style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
            超时 {(r.timeoutMs / 1000).toFixed(0)}s{r.fallbackChannelId ? ' · 有降级' : ''}
          </div>
        </div>
      ),
    },
    {
      title: '匹配条件', key: 'match', width: 220,
      render: (_, r) => (
        <span>
          <Tag>{MODE_TEXT[r.matchMode]}</Tag>
          <span className="gw-mono">{r.pattern}</span>
        </span>
      ),
    },
    {
      title: '分发策略', dataIndex: 'strategy', width: 240,
      render: (v, r) => (
        <span>
          <Tag color={v === 'weight' ? 'blue' : undefined}>{STRATEGY_TEXT[v]}</Tag>
          {r.weights && (
            <span style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
              {Object.entries(r.weights)
                .map(([k, w]) => `${channels.find(c => c.id === Number(k))?.name ?? k} ${w}%`)
                .join(' / ')}
            </span>
          )}
        </span>
      ),
    },
    {
      title: '命中渠道', dataIndex: 'channelIds', width: 220,
      render: v => v.map((id: number) => (
        <Tag key={id} style={{ marginInlineEnd: 4 }}>
          {channels.find(c => c.id === id)?.name ?? id}
        </Tag>
      )),
    },
    {
      title: '重试', dataIndex: 'retry', align: 'right', width: 70,
      render: v => <span className="gw-num">{v}</span>,
    },
    {
      title: '今日命中', dataIndex: 'hit', align: 'right', width: 100,
      render: v => <span className="gw-num">{fmt.n(v)}</span>,
    },
    {
      title: '启用', dataIndex: 'enabled', align: 'center', width: 70,
      render: v => <Switch size="small" checked={v} />,
    },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="路由规则"
        desc="请求进来时按模型名匹配规则；规则优先级高于模型广场内部的供给源顺序"
        extra={<Typography.Link>规则测试</Typography.Link>}
      />

      <div
        style={{
          border: '1px solid var(--gw-border)', borderLeft: '2px solid var(--gw-primary)',
          borderRadius: 6, padding: '10px 12px', fontSize: 13,
          color: 'var(--gw-text-2)', background: 'var(--gw-fill)', marginBottom: 16,
        }}
      >
        拖动左侧手柄调整顺序，规则自上而下匹配，越靠前优先级越高。未命中任何规则的请求将走默认渠道。
      </div>

      <Card>
        <Table<RouteRule>
          rowKey="id"
          size="middle"
          loading={isLoading}
          dataSource={rules}
          columns={columns}
          pagination={false}
          scroll={{ x: 1240 }}
          onRow={onRow}
        />
      </Card>
    </div>
  );
}
