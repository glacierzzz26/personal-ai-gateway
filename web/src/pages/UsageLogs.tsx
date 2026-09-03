import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ReloadOutlined, ThunderboltFilled } from '@ant-design/icons'
import { App as AntdApp, Button, Card, DatePicker, Flex, Input, Select, Space, Switch, Table, Tooltip, Typography } from 'antd'
import type { ColumnsType, TablePaginationConfig } from 'antd/es/table'
import type { SorterResult } from 'antd/es/table/interface'
import dayjs from '../lib/dayjs'
import type { Dayjs } from 'dayjs'
import { errMessage } from '../api/client'
import { listRequests, type RequestsQuery } from '../api/usage'
import { listUpstreams } from '../api/upstreams'
import type { RequestLogRow } from '../api/types'
import { ProtocolTag, StatusTag, statusTier } from '../components/StatusTag'
import { fmtInt, fmtMs, fmtTime, fmtUSD } from '../lib/format'
import { useTheme } from '../theme'

const { RangePicker } = DatePicker
const { Text } = Typography

const BUCKETS = ['2xx', '3xx', '4xx', '5xx']

export default function UsageLogs() {
  const { message } = AntdApp.useApp()
  const { dark } = useTheme()

  const [rows, setRows] = useState<RequestLogRow[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)

  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const [sorter, setSorter] = useState<{ sort?: string; order?: 'asc' | 'desc' }>({})

  const [range, setRange] = useState<[Dayjs, Dayjs] | null>([
    dayjs().subtract(6, 'day').startOf('day'),
    dayjs().endOf('day'),
  ])
  const [protocol, setProtocol] = useState<string>()
  const [model, setModel] = useState<string>()
  const [upstream, setUpstream] = useState<string>()
  const [bucket, setBucket] = useState<string>()
  const [upstreamOpts, setUpstreamOpts] = useState<string[]>([])
  const [auto, setAuto] = useState(false)
  const reqSeq = useRef(0)

  // 上游名下拉(来自订阅源列表);失败不阻塞页面。
  useEffect(() => {
    listUpstreams()
      .then((ups) => setUpstreamOpts(ups.map((u) => u.name)))
      .catch(() => setUpstreamOpts([]))
  }, [])

  const query = useMemo<RequestsQuery>(() => {
    const q: RequestsQuery = { limit: pageSize, offset: (page - 1) * pageSize }
    if (range && range[0] && range[1]) {
      q.from = range[0].startOf('day').utc().format()
      q.to = range[1].endOf('day').utc().format()
    }
    if (protocol) q.protocol = protocol
    if (model) q.model = model
    if (upstream) q.upstream = upstream
    if (bucket) q.status_bucket = bucket
    if (sorter.sort) {
      q.sort = sorter.sort
      q.order = sorter.order
    }
    return q
  }, [page, pageSize, range, protocol, model, upstream, bucket, sorter])

  const load = useCallback(
    async (silent = false) => {
      const id = ++reqSeq.current
      if (!silent) setLoading(true)
      try {
        const resp = await listRequests(query)
        if (id !== reqSeq.current) return
        setRows(resp.data)
        setTotal(resp.meta.total)
      } catch (e) {
        if (id !== reqSeq.current) return
        message.error(errMessage(e))
      } finally {
        if (id === reqSeq.current) setLoading(false)
      }
    },
    [query, message],
  )

  // 查询条件变化即拉取
  useEffect(() => {
    void load()
  }, [load])

  // 自动刷新
  useEffect(() => {
    if (!auto) return
    const t = setInterval(() => void load(true), 15000)
    return () => clearInterval(t)
  }, [auto, load])

  const onTableChange = (pg: TablePaginationConfig, _f: unknown, sr: SorterResult<RequestLogRow> | SorterResult<RequestLogRow>[]) => {
    const s = Array.isArray(sr) ? sr[0] : sr
    setPage(pg.current ?? 1)
    setPageSize(pg.pageSize ?? 20)
    setSorter(s?.order ? { sort: String(s.field ?? 'ts'), order: s.order === 'ascend' ? 'asc' : 'desc' } : {})
  }

  const columns: ColumnsType<RequestLogRow> = useMemo(
    () => [
      {
        title: '时间',
        dataIndex: 'ts',
        width: 168,
        sorter: true,
        defaultSortOrder: undefined,
        render: (v: string) => <span className="num">{fmtTime(v)}</span>,
      },
      { title: '模型', dataIndex: 'model', ellipsis: true, sorter: true },
      { title: '上游', dataIndex: 'upstream', width: 130, sorter: true, render: (v: string) => (v ? <Text className="mono" style={{ fontSize: 13 }}>{v}</Text> : '—') },
      { title: '协议', dataIndex: 'protocol', width: 96, render: (v: string) => <ProtocolTag protocol={v} /> },
      {
        title: '状态',
        dataIndex: 'status',
        width: 100,
        sorter: true,
        render: (v: number, r) => (
          <Space size={4}>
            <StatusTag status={v} />
            {r.stream && (
              <Tooltip title="流式">
                <ThunderboltFilled style={{ color: dark ? '#f5d76a' : '#d4a017' }} />
              </Tooltip>
            )}
          </Space>
        ),
      },
      { title: '输入', dataIndex: 'prompt_tokens', width: 92, align: 'right', sorter: true, render: (v: number) => <span className="num">{fmtInt(v)}</span> },
      { title: '输出', dataIndex: 'completion_tokens', width: 92, align: 'right', sorter: true, render: (v: number) => <span className="num">{fmtInt(v)}</span> },
      { title: '缓存读', dataIndex: 'cache_read_tokens', width: 92, align: 'right', sorter: true, render: (v: number) => <span className="num">{fmtInt(v)}</span> },
      { title: '成本', dataIndex: 'cost', width: 104, align: 'right', sorter: true, render: (v: number) => <span className="num">{fmtUSD(v)}</span> },
      { title: '延迟', dataIndex: 'latency_ms', width: 96, align: 'right', sorter: true, render: (v: number) => <span className="num">{fmtMs(v)}</span> },
    ],
    [dark],
  )

  return (
    <Flex vertical gap={16}>
      <Flex justify="space-between" align="flex-start">
        <div>
          <Text strong style={{ fontSize: 18 }}>
            用量明细
          </Text>
          <br />
          <Text type="secondary">每一次模型请求的元数据与解析到的 token/成本(数据源:request_log)</Text>
        </div>
        <Space>
          <Switch size="small" checked={auto} onChange={setAuto} checkedChildren="自动" unCheckedChildren="自动" />
          <Text type="secondary" style={{ fontSize: 12 }}>
            15s 自动刷新
          </Text>
          <Button icon={<ReloadOutlined spin={loading} />} onClick={() => void load()}>
            刷新
          </Button>
        </Space>
      </Flex>

      <Card className="panel-card" size="small">
        <Space wrap size={8}>
          <RangePicker
            value={range}
            onChange={(v) => setRange(v && v[0] && v[1] ? [v[0], v[1]] : null)}
            allowClear
            presets={[
              { label: '今天', value: [dayjs().startOf('day'), dayjs().endOf('day')] },
              { label: '近 24h', value: [dayjs().subtract(24, 'hour'), dayjs()] },
              { label: '近 7 天', value: [dayjs().subtract(6, 'day').startOf('day'), dayjs().endOf('day')] },
              { label: '近 30 天', value: [dayjs().subtract(29, 'day').startOf('day'), dayjs().endOf('day')] },
            ]}
          />
          <Select
            allowClear
            placeholder="协议"
            style={{ width: 116 }}
            value={protocol}
            onChange={(v) => {
              setProtocol(v)
              setPage(1)
            }}
            options={[
              { value: 'openai', label: 'openai' },
              { value: 'anthropic', label: 'anthropic' },
            ]}
          />
          <Select
            allowClear
            showSearch
            placeholder="上游"
            style={{ width: 180 }}
            value={upstream}
            onChange={(v) => {
              setUpstream(v)
              setPage(1)
            }}
            options={upstreamOpts.map((n) => ({ value: n, label: n }))}
          />
          <Select
            allowClear
            placeholder="状态"
            style={{ width: 120 }}
            value={bucket}
            onChange={(v) => {
              setBucket(v)
              setPage(1)
            }}
            options={BUCKETS.map((b) => ({ value: b, label: b }))}
          />
          <Input.Search
            allowClear
            placeholder="模型名,回车过滤"
            style={{ width: 220 }}
            onSearch={(v) => {
              setModel(v.trim() || undefined)
              setPage(1)
            }}
          />
        </Space>
      </Card>

      <Table<RequestLogRow>
        rowKey="id"
        columns={columns}
        dataSource={rows}
        loading={loading}
        onChange={onTableChange}
        size="middle"
        scroll={{ x: 1180 }}
        expandable={{
          expandedRowRender: (r) => (
            <Flex vertical gap={4} style={{ padding: '4px 12px' }}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                <span className="mono">id={r.id}</span> · 请求方 <span className="mono">{r.client_key || '—'}</span> · 工具{' '}
                <span className="mono">{r.client_tool || '—'}</span> · {statusTier(r.status)}
              </Text>
              {r.error && (
                <Text type="danger" style={{ fontSize: 12 }}>
                  错误:{r.error}
                </Text>
              )}
            </Flex>
          ),
        }}
        pagination={{
          current: page,
          pageSize,
          total,
          showSizeChanger: true,
          pageSizeOptions: [20, 50, 100, 200],
          showTotal: (t) => `共 ${fmtInt(t)} 条`,
          showQuickJumper: true,
        }}
      />
    </Flex>
  )
}
