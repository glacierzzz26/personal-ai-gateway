import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  DollarOutlined,
  FileTextOutlined,
  ReloadOutlined,
  RiseOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { Button, Card, Col, Empty, Flex, Result, Row, Table, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import type { EChartsOption } from 'echarts'
import { errMessage } from '../api/client'
import { fetchSummary } from '../api/usage'
import { listQuota } from '../api/quota'
import type { QuotaRow, SummaryMetrics, SummaryRow } from '../api/types'
import Chart from '../components/Chart'
import StatCard from '../components/StatCard'
import { QuotaAlertCards } from '../components/QuotaAlertCards'
import { fmtInt, fmtUSD, fmtUSDCompact } from '../lib/format'
import dayjs from '../lib/dayjs'
import { useTheme } from '../theme'

const { Title, Text } = Typography

export default function Overview() {
  const { dark } = useTheme()
  const nav = useNavigate()

  const [quotaRows, setQuotaRows] = useState<QuotaRow[] | null>(null)
  const [rolling, setRolling] = useState<SummaryMetrics | null>(null)
  const [dayRows, setDayRows] = useState<SummaryRow[] | null>(null)
  const [topModels, setTopModels] = useState<SummaryRow[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState<string>('')
  const [updatedAt, setUpdatedAt] = useState<string>('')

  const load = useCallback(async () => {
    setLoading(true)
    setErr('')
    const now = dayjs().utc()
    const from14 = now.subtract(13, 'day').startOf('day')
    try {
      const [q, r, d, m] = await Promise.all([
        listQuota(),
        fetchSummary({}), // 默认近 24h
        fetchSummary({ from: from14.format(), to: now.format(), bucket: 'day' }),
        fetchSummary({ group_by: 'model' }), // 近 24h Top 模型
      ])
      setQuotaRows(q)
      setRolling(r.totals)
      setDayRows(d.data)
      setTopModels(m.data)
      setUpdatedAt(dayjs().format('HH:mm:ss'))
    } catch (e) {
      setErr(errMessage(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // 14 天序列:桶对齐,缺桶补 0
  const { days, reqSeries, costSeries, tokIn, tokOut, tokCache } = useMemo(() => {
    const now = dayjs().utc()
    const start = now.subtract(13, 'day').startOf('day')
    const byBucket = new Map<string, SummaryRow>((dayRows ?? []).map((r) => [r.bucket ?? '', r]))
    const days: string[] = []
    const reqSeries: number[] = []
    const costSeries: number[] = []
    const tokIn: number[] = []
    const tokOut: number[] = []
    const tokCache: number[] = []
    for (let i = 0; i < 14; i++) {
      const d = start.add(i, 'day')
      days.push(d.format('MM-DD'))
      const r = byBucket.get(d.format('YYYY-MM-DD'))
      reqSeries.push(r?.requests ?? 0)
      costSeries.push(+(r?.cost ?? 0).toFixed(4))
      tokIn.push(r?.prompt_tokens ?? 0)
      tokOut.push(r?.completion_tokens ?? 0)
      tokCache.push(r?.cache_read_tokens ?? 0)
    }
    return { days, reqSeries, costSeries, tokIn, tokOut, tokCache }
  }, [dayRows])

  const top = useMemo(
    () =>
      (topModels ?? [])
        .slice()
        .sort((a, b) => b.cost - a.cost)
        .slice(0, 6),
    [topModels],
  )

  const axisText = dark ? '#b7bcc7' : '#6b7280'
  const splitLine = dark ? 'rgba(255,255,255,0.09)' : 'rgba(5,5,5,0.06)'
  const tooltipBg = dark ? '#23242b' : '#fff'

  const chart1: EChartsOption = useMemo(
    () => ({
      backgroundColor: 'transparent',
      tooltip: { trigger: 'axis', backgroundColor: tooltipBg, borderWidth: 0, textStyle: { color: axisText } },
      legend: { top: 0, right: 0, textStyle: { color: axisText } },
      grid: { left: 8, right: 8, top: 34, bottom: 0, containLabel: true },
      xAxis: {
        type: 'category',
        data: days,
        axisLine: { lineStyle: { color: splitLine } },
        axisLabel: { color: axisText },
      },
      yAxis: [
        { type: 'value', minInterval: 1, axisLabel: { color: axisText }, splitLine: { lineStyle: { color: splitLine } } },
        { type: 'value', axisLabel: { color: axisText, formatter: fmtUSDCompact }, splitLine: { show: false } },
      ],
      series: [
        {
          name: '请求数',
          type: 'bar',
          data: reqSeries,
          barMaxWidth: 20,
          itemStyle: { color: '#4c5fe0', borderRadius: [4, 4, 0, 0] },
        },
        {
          name: '成本($)',
          type: 'line',
          yAxisIndex: 1,
          smooth: true,
          symbol: 'circle',
          symbolSize: 6,
          data: costSeries,
          itemStyle: { color: '#f59e0b' },
          lineStyle: { width: 2.4 },
        },
      ],
    }),
    [days, reqSeries, costSeries, axisText, splitLine, tooltipBg],
  )

  const chart2: EChartsOption = useMemo(
    () => ({
      backgroundColor: 'transparent',
      tooltip: { trigger: 'axis', backgroundColor: tooltipBg, borderWidth: 0, textStyle: { color: axisText } },
      legend: { top: 0, right: 0, textStyle: { color: axisText } },
      grid: { left: 8, right: 8, top: 34, bottom: 0, containLabel: true },
      xAxis: {
        type: 'category',
        data: days,
        boundaryGap: false,
        axisLine: { lineStyle: { color: splitLine } },
        axisLabel: { color: axisText },
      },
      yAxis: {
        type: 'value',
        axisLabel: { color: axisText, formatter: (v: number) => (v >= 1e6 ? `${(v / 1e6).toFixed(1)}M` : v >= 1e3 ? `${(v / 1e3).toFixed(0)}k` : String(v)) },
        splitLine: { lineStyle: { color: splitLine } },
      },
      series: [
        { name: '输入', type: 'line', data: tokIn, smooth: true, areaStyle: { opacity: 0.16 }, itemStyle: { color: '#4c5fe0' }, stack: 't' },
        { name: '输出', type: 'line', data: tokOut, smooth: true, areaStyle: { opacity: 0.16 }, itemStyle: { color: '#2dd4bf' }, stack: 't' },
        { name: '缓存读', type: 'line', data: tokCache, smooth: true, areaStyle: { opacity: 0.16 }, itemStyle: { color: '#b37feb' }, stack: 't' },
      ],
    }),
    [days, tokIn, tokOut, tokCache, axisText, splitLine, tooltipBg],
  )

  const totalTokens = (rolling ? rolling.prompt_tokens + rolling.completion_tokens + rolling.cache_read_tokens : 0)
  const errorRate = rolling && rolling.requests > 0 ? (rolling.errors / rolling.requests) * 100 : 0

  const modelCols: ColumnsType<SummaryRow> = [
    { title: '模型', dataIndex: 'model', ellipsis: true },
    { title: '请求', dataIndex: 'requests', align: 'right', width: 90, render: (v: number) => fmtInt(v) },
    { title: '输入', dataIndex: 'prompt_tokens', align: 'right', width: 100, render: (v: number) => fmtInt(v) },
    { title: '输出', dataIndex: 'completion_tokens', align: 'right', width: 100, render: (v: number) => fmtInt(v) },
    { title: '成本', dataIndex: 'cost', align: 'right', width: 110, render: (v: number) => <span className="num">{fmtUSD(v)}</span> },
  ]

  return (
    <Flex vertical gap={20}>
      <Flex justify="space-between" align="flex-start">
        <div>
          <Title level={4} style={{ marginBottom: 4 }}>
            概览
          </Title>
          <Text type="secondary">近 24 小时用量与全上游配额状态{updatedAt && ` · 更新于 ${updatedAt}`}</Text>
        </div>
        <Button icon={<ReloadOutlined spin={loading} />} onClick={() => void load()}>
          刷新
        </Button>
      </Flex>

      {err ? (
        <Card>
          <Result status="warning" title="数据加载失败" subTitle={err} extra={<Button type="primary" onClick={() => void load()}>重试</Button>} />
        </Card>
      ) : (
        <>
          <Row gutter={[16, 16]}>
            <Col xs={24} sm={12} xl={6}>
              <StatCard title="近 24h 请求数" value={rolling?.requests} icon={<RiseOutlined />} color="#4c5fe0" loading={loading} />
            </Col>
            <Col xs={24} sm={12} xl={6}>
              <StatCard title="总 tokens(入/出/缓存)" value={totalTokens} icon={<FileTextOutlined />} color="#0ea5e9" loading={loading} />
            </Col>
            <Col xs={24} sm={12} xl={6}>
              <StatCard title="近 24h 成本" value={rolling?.cost} precision={4} prefix="$" icon={<DollarOutlined />} color="#f59e0b" loading={loading} />
            </Col>
            <Col xs={24} sm={12} xl={6}>
              <StatCard
                title="错误数(≥4xx)"
                value={rolling?.errors}
                suffix={rolling && rolling.requests > 0 ? <Text type="secondary" style={{ fontSize: 14 }}>({errorRate.toFixed(2)}%)</Text> : undefined}
                icon={<WarningOutlined />}
                color="#ef4444"
                loading={loading}
              />
            </Col>
          </Row>

          <Card className="panel-card" title="异常与告警" extra={quotaRows ? <Button type="link" onClick={() => nav('/quota')}>查看全部配额 →</Button> : undefined}>
            {quotaRows ? (
              <QuotaAlertCards
                rows={quotaRows}
                action={(issue) =>
                  issue.severity === 'error' ? (
                    <Button size="small" type="link" onClick={() => nav('/quota')}>处理</Button>
                  ) : undefined
                }
              />
            ) : (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="配额数据加载中…" />
            )}
          </Card>

          <Row gutter={[16, 16]}>
            <Col xs={24} lg={12}>
              <Card className="chart-card" title="近 14 天请求与成本">
                <Chart option={chart1} height={300} />
              </Card>
            </Col>
            <Col xs={24} lg={12}>
              <Card className="chart-card" title="近 14 天 tokens 趋势">
                <Chart option={chart2} height={300} />
              </Card>
            </Col>
          </Row>

          <Card className="panel-card" title="Top 模型 · 近 24h 成本">
            <Table<SummaryRow>
              rowKey="model"
              columns={modelCols}
              dataSource={top}
              pagination={false}
              size="small"
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="近 24h 暂无模型请求" /> }}
            />
          </Card>
        </>
      )}
    </Flex>
  )
}
