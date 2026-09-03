import { useCallback, useEffect, useMemo, useState } from 'react'
import { DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined, ThunderboltOutlined } from '@ant-design/icons'
import {
  App as AntdApp,
  Button,
  Card,
  Col,
  Divider,
  Empty,
  Flex,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Result,
  Row,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { errMessage } from '../api/client'
import { createUpstream, deleteUpstream, listUpstreams, testUpstream, updateUpstream } from '../api/upstreams'
import type { PingResult, QuotaCfg, Upstream, UpstreamType } from '../api/types'
import { UpstreamTypeTag } from '../components/StatusTag'

const { Text } = Typography

interface QuotaFields {
  window: string
  warn_used_pct: number
  hard_used_pct: number
  cache_ttl_sec: number
  invert_used_pct: boolean
}

interface UpstreamForm {
  name: string
  type: UpstreamType
  base_url: string
  api_key?: string
  priority: number
  models: string[]
  quotaEnabled: boolean
  quota: QuotaFields
  cooldown_sec: number
  max_failures: number
}

const DEFAULTS: UpstreamForm = {
  name: '',
  type: 'openai',
  base_url: '',
  api_key: '',
  priority: 1,
  models: [],
  quotaEnabled: false,
  quota: { window: 'monthly', warn_used_pct: 80, hard_used_pct: 95, cache_ttl_sec: 60, invert_used_pct: false },
  cooldown_sec: 10,
  max_failures: 3,
}

function resultView(res: PingResult) {
  if (!res.reachable) return { status: 'error' as const, title: '不可达' }
  if (res.status != null && res.status < 400) return { status: 'success' as const, title: '连通正常' }
  if (res.status === 401 || res.status === 403) return { status: 'warning' as const, title: '可达,但密钥被拒绝' }
  return { status: 'warning' as const, title: `可达(HTTP ${res.status ?? '?'})` }
}

export default function UpstreamsPage() {
  const { message } = AntdApp.useApp()
  const [rows, setRows] = useState<Upstream[] | null>(null)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)

  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Upstream | null>(null)
  const [saving, setSaving] = useState(false)
  const [form] = Form.useForm<UpstreamForm>()

  const [test, setTest] = useState<{ open: boolean; name: string; loading: boolean; result: PingResult | null }>({ open: false, name: '', loading: false, result: null })

  const load = useCallback(async () => {
    setLoading(true)
    setErr('')
    try {
      setRows(await listUpstreams())
    } catch (e) {
      setErr(errMessage(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // ---------- 表单开合 ----------
  useEffect(() => {
    if (!open) return
    form.resetFields()
    if (editing) {
      form.setFieldsValue({
        name: editing.name,
        type: editing.type,
        base_url: editing.base_url,
        api_key: '',
        priority: editing.priority,
        models: editing.models ?? [],
        quotaEnabled: !!editing.quota?.enabled,
        quota: editing.quota
          ? {
              window: editing.quota.window,
              warn_used_pct: editing.quota.warn_used_pct,
              hard_used_pct: editing.quota.hard_used_pct,
              cache_ttl_sec: editing.quota.cache_ttl_sec,
              invert_used_pct: editing.quota.invert_used_pct,
            }
          : { ...DEFAULTS.quota },
        cooldown_sec: editing.cooldown_sec,
        max_failures: editing.max_failures,
      })
    } else {
      form.setFieldsValue(DEFAULTS)
    }
  }, [open, editing, form])

  const openCreate = () => {
    setEditing(null)
    setOpen(true)
  }
  const openEdit = (r: Upstream) => {
    setEditing(r)
    setOpen(true)
  }

  const typeWatch = Form.useWatch('type', form)
  const modelsWatch = Form.useWatch('models', form) ?? []
  const quotaOn = Form.useWatch('quotaEnabled', form)
  const wildcard = modelsWatch.includes('*')

  const baseHint =
    typeWatch === 'openai'
      ? 'type: openai → base_url 需包含 /v1,出站拼 /chat/completions、/models'
      : 'type: anthropic → base_url 为域名根,出站拼 /v1/messages、/v1/models'

  const submit = async () => {
    let v: UpstreamForm
    try {
      v = await form.validateFields()
    } catch {
      return
    }
    const payload: Upstream = {
      name: v.name.trim(),
      type: v.type,
      base_url: v.base_url.trim(),
      api_key: (v.api_key ?? '').trim(),
      priority: v.priority,
      models: v.models?.length ? v.models.map((m) => m.trim()).filter(Boolean) : [],
      cooldown_sec: v.cooldown_sec,
      max_failures: v.max_failures,
      quota: v.quotaEnabled
        ? {
            enabled: true,
            window: v.quota.window,
            warn_used_pct: v.quota.warn_used_pct,
            hard_used_pct: v.quota.hard_used_pct,
            cache_ttl_sec: v.quota.cache_ttl_sec,
            invert_used_pct: !!v.quota.invert_used_pct,
          }
        : null,
    }
    setSaving(true)
    try {
      if (editing) {
        await updateUpstream(editing.name, payload)
        message.success(`已更新订阅源「${editing.name}」,变更即时生效`)
      } else {
        const created = await createUpstream(payload)
        message.success(`已新增订阅源「${created.name}」,可点击行内「连通测试」验证`)
      }
      setOpen(false)
      void load()
    } catch (e) {
      message.error(errMessage(e))
    } finally {
      setSaving(false)
    }
  }

  const remove = async (r: Upstream) => {
    try {
      await deleteUpstream(r.name)
      message.success(`已删除订阅源「${r.name}」`)
      void load()
    } catch (e) {
      message.error(errMessage(e))
    }
  }

  const runTest = async (name: string) => {
    setTest({ open: true, name, loading: true, result: null })
    try {
      const res = await testUpstream(name)
      setTest({ open: true, name, loading: false, result: res })
    } catch (e) {
      setTest({ open: false, name: '', loading: false, result: null })
      message.error(errMessage(e))
    }
  }

  const quotaCell = (q: QuotaCfg | null) =>
    q && q.enabled ? (
      <Space size={4}>
        <Tag color="gold">{q.window}</Tag>
        <Text type="secondary" style={{ fontSize: 12 }}>
          {q.warn_used_pct}/{q.hard_used_pct}%
        </Text>
      </Space>
    ) : (
      <Tag>未启用</Tag>
    )

  const columns: ColumnsType<Upstream> = useMemo(
    () => [
      {
        title: '名称',
        dataIndex: 'name',
        width: 150,
        render: (v: string) => <Text strong className="mono">{v}</Text>,
      },
      { title: '类型', dataIndex: 'type', width: 110, render: (v: UpstreamType) => <UpstreamTypeTag type={v} /> },
      {
        title: 'base_url',
        dataIndex: 'base_url',
        ellipsis: true,
        render: (v: string) => (
          <Tooltip title={v}>
            <Text className="mono" style={{ fontSize: 13 }}>{v}</Text>
          </Tooltip>
        ),
      },
      {
        title: 'api_key',
        dataIndex: 'api_key',
        width: 170,
        render: (v: string) => (
          <Tooltip title="仅存 ${ENV} 引用或掩码,明文密钥不回显、不落前端">
            <Text type="secondary" className="mono" style={{ fontSize: 13 }}>{v || '—'}</Text>
          </Tooltip>
        ),
      },
      { title: '优先级', dataIndex: 'priority', width: 80, align: 'center' },
      {
        title: '模型',
        dataIndex: 'models',
        width: 150,
        render: (v: string[] | null) => {
          const list = v ?? []
          if (list.length === 0) return <Tag>全部(*)</Tag>
          const shown = list.slice(0, 2)
          return (
            <Space size={4} wrap>
              {shown.map((m) => (
                <Tag key={m} style={{ maxWidth: 120, overflow: 'hidden', textOverflow: 'ellipsis' }}>{m}</Tag>
              ))}
              {list.length > 2 && <Text type="secondary">+{list.length - 2}</Text>}
            </Space>
          )
        },
      },
      { title: '配额', dataIndex: 'quota', width: 150, render: (q: QuotaCfg | null) => quotaCell(q) },
      {
        title: '操作',
        key: 'actions',
        width: 230,
        render: (_, r) => (
          <Space size={0}>
            <Button type="link" size="small" icon={<ThunderboltOutlined />} loading={test.name === r.name && test.loading} onClick={() => void runTest(r.name)}>
              测试
            </Button>
            <Button type="link" size="small" icon={<EditOutlined />} onClick={() => openEdit(r)}>
              编辑
            </Button>
            <Popconfirm
              title={`删除订阅源「${r.name}」?`}
              description="网关将即时失去该上游,正在进行的请求不会回退;此操作不可撤销。"
              okText="删除"
              cancelText="取消"
              okButtonProps={{ danger: true }}
              onConfirm={() => void remove(r)}
            >
              <Button type="link" size="small" danger icon={<DeleteOutlined />}>
                删除
              </Button>
            </Popconfirm>
          </Space>
        ),
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [test.name, test.loading],
  )

  const testView = test.result ? resultView(test.result) : null

  return (
    <Flex vertical gap={16}>
      <Flex justify="space-between" align="flex-start">
        <div>
          <Text strong style={{ fontSize: 18 }}>
            订阅源
          </Text>
          <br />
          <Text type="secondary">运行时唯一来源:gateway.db 的 upstreams 表;config.yaml 仅首次播种。增删改即时生效,无需重启。</Text>
        </div>
        <Space>
          <Button icon={<ReloadOutlined spin={loading} />} onClick={() => void load()}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新增订阅源
          </Button>
        </Space>
      </Flex>

      {err ? (
        <Card>
          <Result status="warning" title="订阅源加载失败" subTitle={err} extra={<Button type="primary" onClick={() => void load()}>重试</Button>} />
        </Card>
      ) : (
        <Table<Upstream>
          rowKey="name"
          columns={columns}
          dataSource={rows ?? []}
          loading={loading && rows == null}
          pagination={false}
          size="middle"
          scroll={{ x: 1100 }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有订阅源,点右上角新增" /> }}
        />
      )}

      {/* 新增 / 编辑 */}
      <Modal
        title={editing ? `编辑订阅源 · ${editing.name}` : '新增订阅源'}
        open={open}
        onOk={() => void submit()}
        onCancel={() => setOpen(false)}
        confirmLoading={saving}
        okText={editing ? '保存' : '新增'}
        cancelText="取消"
        width={600}
      >
        <Form form={form} layout="vertical" requiredMark="optional" style={{ marginTop: 12 }}>
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="name"
                label="名称"
                rules={[
                  { required: true, message: '请输入名称' },
                  { pattern: /^[a-zA-Z0-9._-]+$/, message: '仅限字母/数字/._-' },
                ]}
                extra={editing ? '名称不可修改(取自 API 路径)' : '唯一标识,创建后不可改'}
              >
                <Input placeholder="second-sub" disabled={!!editing} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="type" label="协议类型" rules={[{ required: true }]}>
                <Select
                  options={[
                    { value: 'openai', label: 'openai(含 /v1)' },
                    { value: 'anthropic', label: 'anthropic(域名根)' },
                  ]}
                />
              </Form.Item>
            </Col>
          </Row>

          <Form.Item name="base_url" label="base_url" rules={[{ required: true, message: '请输入 base_url' }, { pattern: /^https?:\/\/.+/, message: '需以 http(s):// 开头' }]} extra={baseHint}>
            <Input
              placeholder={typeWatch === 'openai' ? 'https://your-upstream/v1' : 'https://api.anthropic.com'}
            />
          </Form.Item>

          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="api_key"
                label="api_key"
                rules={editing ? undefined : [{ required: true, message: 'api_key 必填' }]}
                extra={editing ? '留空 = 保持原密钥,无需重贴' : '可写字面值或 ${ENV} 引用,原样入库'}
              >
                <Input.Password placeholder={editing ? '(留空保持不变)' : 'sk-… 或 ${MY_KEY}'} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="priority" label="优先级" tooltip="越小越优先;配额硬 / 熔断会把该上游降级" rules={[{ required: true }]}>
                <InputNumber min={0} max={999} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
          </Row>

          <Row gutter={16} align="top">
            <Col span={16}>
              <Form.Item name="models" label="可出模型" extra="支持前缀通配 claude-*;留空 = 全部。已选含 * 时等同于全部">
                <Select
                  mode="tags"
                  placeholder="输入后回车,如 claude-sonnet-*"
                  disabled={wildcard}
                  open={false}
                  suffixIcon={null}
                  tokenSeparators={[',', ' ', '，']}
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item label="全部(*)" style={{ marginBottom: 0 }}>
                <Flex align="center" style={{ paddingTop: 6 }} gap={8}>
                  <Switch
                    checked={wildcard}
                    onChange={(on) => form.setFieldValue('models', on ? ['*'] : [])}
                  />
                  <Text type="secondary" style={{ fontSize: 12 }}>匹配所有模型</Text>
                </Flex>
              </Form.Item>
            </Col>
          </Row>

          <Divider style={{ margin: '8px 0 16px' }} />
          <Flex justify="space-between" align="center" style={{ marginBottom: 12 }}>
            <Text strong>配额监控(可选)</Text>
            <Form.Item name="quotaEnabled" valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
          </Flex>
          {quotaOn && (
            <Row gutter={16}>
              <Col span={12}>
                <Form.Item name={['quota', 'window']} label="用量窗口" rules={[{ required: true }]}>
                  <Select
                    options={[
                      { value: 'monthly', label: 'monthly(自然月)' },
                      { value: 'weekly', label: 'weekly(自然周)' },
                      { value: 'rolling', label: 'rolling(滚动)' },
                    ]}
                  />
                </Form.Item>
              </Col>
              <Col span={12}>
                <Form.Item name={['quota', 'invert_used_pct']} label="percent 表示剩余" valuePropName="checked" tooltip="上游返回的 percent 若是“剩余量”而非“已用”时开启">
                  <Switch />
                </Form.Item>
              </Col>
              <Col span={8}>
                <Form.Item name={['quota', 'warn_used_pct']} label="告警阈值 %" rules={[{ required: true }]}>
                  <InputNumber min={1} max={100} style={{ width: '100%' }} addonAfter="%" />
                </Form.Item>
              </Col>
              <Col span={8}>
                <Form.Item name={['quota', 'hard_used_pct']} label="硬阈值 %" rules={[{ required: true }]} tooltip="达到后该上游从首选降为备选">
                  <InputNumber min={1} max={100} style={{ width: '100%' }} addonAfter="%" />
                </Form.Item>
              </Col>
              <Col span={8}>
                <Form.Item name={['quota', 'cache_ttl_sec']} label="拉取周期 s" rules={[{ required: true }]}>
                  <InputNumber min={5} max={3600} style={{ width: '100%' }} addonAfter="s" />
                </Form.Item>
              </Col>
            </Row>
          )}

          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="cooldown_sec" label="熔断冷却 s" tooltip="连续失败后进入冷却的秒数" rules={[{ required: true }]}>
                <InputNumber min={1} max={86400} style={{ width: '100%' }} addonAfter="s" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="max_failures" label="连续失败上限" tooltip="达到后熔断进入冷却" rules={[{ required: true }]}>
                <InputNumber min={1} max={100} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      </Modal>

      {/* 连通测试结果 */}
      <Modal
        title={<>连通测试 · {test.name || ''}</>}
        open={test.open}
        onCancel={() => setTest({ open: false, name: '', loading: false, result: null })}
        footer={[
          <Button
            key="again"
            icon={<ThunderboltOutlined />}
            onClick={() => test.name && void runTest(test.name)}
            loading={test.loading}
          >
            再测一次
          </Button>,
          <Button key="close" type="primary" onClick={() => setTest({ open: false, name: '', loading: false, result: null })}>
            关闭
          </Button>,
        ]}
      >
        {test.loading ? (
          <Flex justify="center" style={{ padding: 32 }}>
            <Text type="secondary">正在探测 {test.name} 的 /models …</Text>
          </Flex>
        ) : test.result && testView ? (
          <Result status={testView.status} title={testView.title} subTitle={test.result.message} />
        ) : null}
      </Modal>
    </Flex>
  )
}
