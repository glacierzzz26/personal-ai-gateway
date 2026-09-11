import { useState } from 'react';
import {
  Alert, Button, Card, Empty, Input, Select, Table, Tag,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { ReloadOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import PageHeader from '@/components/PageHeader';
import RequestLogDrawer from '@/components/RequestLogDrawer';
import { api } from '@/services/api';
import { fmt } from '@/utils/format';
import type { LogFilters, RequestLogItem } from '@/types';

const PAGE_SIZES = [20, 50, 100];

/** 请求日志:筛选/分页全部走后端 GET /logs(状态筛选 ok/error 语义与后端一致)。 */
export default function Logs() {
  const [filters, setFilters] = useState<LogFilters>({});
  const [kwInput, setKwInput] = useState('');
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [detail, setDetail] = useState<RequestLogItem | null>(null);

  // 任一筛选变化都回到第一页(关键字在搜索时提交,不逐键触发)
  const applyFilter = (next: Partial<LogFilters>) => {
    setFilters(f => ({ ...f, ...next }));
    setPage(1);
  };

  // 筛选下拉数据源:真实渠道/模型名
  const { data: channelList = [] } = useQuery({ queryKey: ['channels'], queryFn: api.getChannels });
  const { data: modelList = [] } = useQuery({ queryKey: ['models'], queryFn: api.getModels });

  const { data, isLoading, isFetching, refetch, isError, error } = useQuery({
    queryKey: ['logs', filters, page, size],
    queryFn: () => api.getLogs(filters, page, size),
  });
  const logs = data?.items ?? [];
  const total = data?.total ?? 0;
  const hasFilter = !!(filters.model || filters.channel || filters.status || filters.kw);

  const reset = () => {
    setFilters({});
    setKwInput('');
    setPage(1);
  };

  const okCode = (code: number) => code >= 100 && code < 400;
  // 499 = 客户端主动断开(按 Esc/断网),既非成功也非故障,单列以免污染错误率观感。
  const CANCELED = 499;

  const columns: ColumnsType<RequestLogItem> = [
    {
      title: '时间', dataIndex: 'ts', width: 142,
      render: v => <span className="gw-mono" style={{ fontSize: 12 }}>{v}</span>,
    },
    {
      title: '状态', dataIndex: 'statusCode', width: 84,
      render: (v, r) => {
        if (v === CANCELED) {
          return <Tag color="default" style={{ marginInlineEnd: 0 }}>中断</Tag>;
        }
        const ok = okCode(v);
        return (
          <Tag color={ok ? 'success' : 'error'} style={{ marginInlineEnd: 0 }}>
            {ok ? '成功' : (r.error ? '失败' : String(v))}
          </Tag>
        );
      },
    },
    { title: '模型', dataIndex: 'model', render: v => <span className="gw-mono">{v}</span> },
    { title: '渠道', dataIndex: 'channelName', width: 150, ellipsis: true },
    { title: '令牌', dataIndex: 'tokenName', width: 130, ellipsis: true },
    {
      title: '输入', dataIndex: 'inTokens', align: 'right',
      render: v => <span className="gw-num">{fmt.k(v)}</span>,
    },
    {
      title: '输出', dataIndex: 'outTokens', align: 'right',
      render: v => <span className="gw-num">{fmt.k(v)}</span>,
    },
    {
      title: '缓存', dataIndex: 'cacheReadTokens', align: 'right', width: 70,
      render: v => (v ? <span className="gw-num">{fmt.k(v)}</span> : <span style={{ color: 'var(--gw-text-3)' }}>—</span>),
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
  ];

  const errorText = error instanceof Error ? error.message : String(error ?? '加载失败');

  return (
    <div className="gw-page">
      <PageHeader
        title="请求日志"
        desc="逐条请求明细，筛选与分页均由服务端执行"
        extra={
          <Button icon={<ReloadOutlined />} loading={isFetching} onClick={() => refetch()}>刷新</Button>
        }
      />

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center', marginBottom: 16 }}>
        <Select
          style={{ width: 220 }}
          value={filters.model}
          onChange={v => applyFilter({ model: v })}
          showSearch
          allowClear
          placeholder="全部模型"
          options={modelList.map(m => ({ value: m.name, label: m.name }))}
          filterOption={(input, o) => String(o?.label ?? '').toLowerCase().includes(input.toLowerCase())}
        />
        <Select
          style={{ width: 180 }}
          value={filters.channel}
          onChange={v => applyFilter({ channel: v })}
          showSearch
          allowClear
          placeholder="全部渠道"
          options={channelList.map(c => ({ value: c.name, label: c.name }))}
          filterOption={(input, o) => String(o?.label ?? '').toLowerCase().includes(input.toLowerCase())}
        />
        <Select
          style={{ width: 130 }}
          value={filters.status || undefined}
          onChange={v => applyFilter({ status: v ?? '' })}
          placeholder="全部状态"
          options={[
            { value: 'ok', label: '成功' },
            { value: 'error', label: '失败' },
            { value: 'canceled', label: '中断' },
          ]}
        />
        <Input.Search
          allowClear
          placeholder="搜索模型/渠道/令牌/IP/错误"
          style={{ width: 220 }}
          value={kwInput}
          onChange={e => {
            const val = e.target.value;
            setKwInput(val);
            // 点清除按钮时同步清掉已生效的关键词筛选
            if (!val && filters.kw) applyFilter({ kw: '' });
          }}
          onSearch={v => applyFilter({ kw: v.trim() })}
        />
        <Button onClick={reset}>重置</Button>
        {(total > 0 || hasFilter) && (
          <span style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
            {hasFilter ? `筛选出 ${fmt.n(total)} 条` : `共 ${fmt.n(total)} 条`}
          </span>
        )}
      </div>

      {isError && (
        <Alert
          type="error"
          showIcon
          message="日志加载失败"
          description={errorText}
          action={<Button size="small" onClick={() => refetch()}>重试</Button>}
          style={{ marginBottom: 16 }}
        />
      )}

      <Card>
        <Table<RequestLogItem>
          rowKey="id"
          size="middle"
          loading={isLoading || isFetching}
          dataSource={logs}
          columns={columns}
          scroll={{ x: 1280 }}
          locale={{ emptyText: <Empty description={hasFilter ? '没有符合筛选条件的日志' : '暂无请求日志'} /> }}
          onRow={r => ({
            onClick: () => setDetail(r),
            style: { cursor: 'pointer' },
          })}
          pagination={{
            current: page,
            pageSize: size,
            total,
            showSizeChanger: true,
            pageSizeOptions: PAGE_SIZES.map(String),
            showTotal: t => `共 ${fmt.n(t)} 条`,
            onChange: (p, ps) => {
              // 切换 pageSize 时回到第 1 页,避免超出新分页范围
              setPage(ps !== size ? 1 : p);
              setSize(ps);
            },
          }}
        />
      </Card>

      <RequestLogDrawer detail={detail} onClose={() => setDetail(null)} />
    </div>
  );
}
