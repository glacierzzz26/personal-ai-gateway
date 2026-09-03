import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ReloadOutlined, SettingOutlined, SyncOutlined } from '@ant-design/icons'
import { Button, Card, Col, Empty, Flex, Progress, Result, Row, Tag, Typography, theme } from 'antd'
import { errMessage } from '../api/client'
import { listQuota } from '../api/quota'
import type { QuotaRow } from '../api/types'
import { QuotaAlertCards } from '../components/QuotaAlertCards'
import { UpstreamTypeTag } from '../components/StatusTag'
import { fmtCountdown, fmtTimeShort } from '../lib/format'

const { Text } = Typography

function pctColor(r: QuotaRow): string {
  if (r.hard === true || (r.status != null && r.status !== 'ok')) return '#f5222d'
  if ((r.used_pct ?? 0) >= (r.warn_used_pct ?? 80)) return '#faad14'
  return '#52c41a'
}

function StatusBadge({ r }: { r: QuotaRow }) {
  if (r.status == null) return <Tag>未拉取</Tag>
  const ok = r.status === 'ok'
  return <Tag color={ok ? 'green' : 'red'}>{ok ? 'ok' : r.status}</Tag>
}

export default function QuotaStatusPage() {
  const nav = useNavigate()
  const { token } = theme.useToken()
  const [rows, setRows] = useState<QuotaRow[] | null>(null)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    setErr('')
    try {
      setRows(await listQuota())
    } catch (e) {
      setErr(errMessage(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const enabledCount = (rows ?? []).filter((r) => r.enabled).length

  return (
    <Flex vertical gap={16}>
      <Flex justify="space-between" align="flex-start">
        <div>
          <Text strong style={{ fontSize: 18 }}>
            配额与告警
          </Text>
          <br />
          <Text type="secondary">
            已启用配额监控的订阅源 {enabledCount} 个 · 告警阈值 warning 级不改变选路,hard(耗尽或状态异常)会把该上游从首选降为备选
          </Text>
        </div>
        <Button icon={<ReloadOutlined spin={loading} />} onClick={() => void load()}>
          刷新
        </Button>
      </Flex>

      {err ? (
        <Card>
          <Result status="warning" title="配额数据加载失败" subTitle={err} extra={<Button type="primary" onClick={() => void load()}>重试</Button>} />
        </Card>
      ) : (
        <>
          <Card className="panel-card" size="small" title="告警">
            {rows ? (
              <QuotaAlertCards
                rows={rows}
                action={(issue) =>
                  issue.severity === 'error' ? (
                    <Button size="small" type="link" icon={<SettingOutlined />} onClick={() => nav('/upstreams')}>
                      检查订阅源
                    </Button>
                  ) : undefined
                }
              />
            ) : (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="加载中…" />
            )}
          </Card>

          {!rows && loading ? (
            <Card loading />
          ) : (
            <Row gutter={[16, 16]}>
              {(rows ?? []).map((r) => (
                <Col key={r.upstream} xs={24} md={12} xl={8}>
                  <Card className="panel-card" title={r.upstream} extra={<UpstreamTypeTag type={r.type} />}>
                    {!r.enabled ? (
                      <Flex vertical gap={4} align="center" style={{ padding: '12px 0', opacity: 0.6 }}>
                        <SyncOutlined style={{ fontSize: 22 }} />
                        <Text>未启用配额监控</Text>
                        <Text type="secondary" style={{ fontSize: 12 }}>
                          如需按用量自动切换,请在订阅源配置中开启 quota(上游需暴露用量接口)
                        </Text>
                      </Flex>
                    ) : r.used_pct == null ? (
                      <Flex vertical gap={6} align="center" style={{ padding: '12px 0' }}>
                        <Progress type="circle" size={110} percent={0} strokeColor={token.colorPrimary} format={() => <Text type="secondary">等待中</Text>} />
                        <Text type="secondary">尚未拉到用量快照({r.window ?? ''}窗口),请稍候或检查上游用量接口</Text>
                      </Flex>
                    ) : (
                      <Flex vertical gap={12}>
                        <Flex justify="space-between" align="center">
                          <Progress
                            type="dashboard"
                            size={118}
                            percent={r.used_pct}
                            strokeColor={pctColor(r)}
                            format={(p) => (
                              <div>
                                <div style={{ fontSize: 22, fontWeight: 700 }}>{p}%</div>
                                <div style={{ fontSize: 11, opacity: 0.6 }}>已用</div>
                              </div>
                            )}
                          />
                          <Flex vertical gap={6} align="flex-end" style={{ minWidth: 130 }}>
                            <StatusBadge r={r} />
                            <Tag color={r.hard ? 'red' : 'default'}>{r.hard ? 'hard(降为备选)' : '正常候选'}</Tag>
                            <Text type="secondary" style={{ fontSize: 12 }}>
                              阈值 {r.warn_used_pct}% / {r.hard_used_pct}%
                            </Text>
                          </Flex>
                        </Flex>
                        <Flex justify="space-between" align="center">
                          <Text type="secondary" style={{ fontSize: 12 }}>
                            窗口:{r.window} · 重置于 {r.resets_at ? `${fmtTimeShort(r.resets_at)}(${fmtCountdown(r.resets_at)})` : '—'}
                          </Text>
                          <Button size="small" type="link" onClick={() => nav('/upstreams')}>
                            调整
                          </Button>
                        </Flex>
                      </Flex>
                    )}
                  </Card>
                </Col>
              ))}
            </Row>
          )}

          {rows && rows.length === 0 && (
            <Card>
              <Empty description={`当前没有订阅源(网关至少需要一个上游)`} />
            </Card>
          )}
        </>
      )}
    </Flex>
  )
}
