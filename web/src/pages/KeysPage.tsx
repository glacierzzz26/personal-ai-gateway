import { useCallback, useEffect, useMemo, useState } from 'react'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { Alert, App as AntdApp, Button, Card, Empty, Flex, Form, Input, Modal, Popconfirm, Result, Space, Table, Tag, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { errMessage } from '../api/client'
import { createKey, listKeys, revokeKey } from '../api/keys'
import type { ApiKeyRow } from '../api/types'

const { Text } = Typography

function fmtTime(v: string): string {
  if (!v) return '—'
  const d = new Date(v)
  return isNaN(d.getTime()) ? v : d.toLocaleString()
}

export default function KeysPage() {
  const { message } = AntdApp.useApp()
  const [rows, setRows] = useState<ApiKeyRow[] | null>(null)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)

  const [open, setOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [form] = Form.useForm<{ name: string; note?: string }>()

  // 创建成功的一次性明文:仅存组件内存,刷新即丢 —— 设计如此,secret 只在 201 出现一次。
  const [lastSecret, setLastSecret] = useState<{ name: string; secret: string } | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setErr('')
    try {
      setRows(await listKeys())
    } catch (e) {
      setErr(errMessage(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (open) form.resetFields()
  }, [open, form])

  const submit = async () => {
    let v: { name: string; note?: string }
    try {
      v = await form.validateFields()
    } catch {
      return
    }
    setSaving(true)
    try {
      const created = await createKey(v.name.trim(), (v.note ?? '').trim())
      setLastSecret({ name: created.name, secret: created.secret })
      setOpen(false)
      void load()
    } catch (e) {
      message.error(errMessage(e))
    } finally {
      setSaving(false)
    }
  }

  const revoke = async (r: ApiKeyRow) => {
    try {
      await revokeKey(r.name)
      message.success(`已吊销「${r.name}」,即刻失效`)
      void load()
    } catch (e) {
      message.error(errMessage(e))
    }
  }

  const columns: ColumnsType<ApiKeyRow> = useMemo(
    () => [
      {
        title: '名称',
        dataIndex: 'name',
        width: 160,
        render: (v: string) => <Text strong className="mono">{v}</Text>,
      },
      {
        title: '前缀(展示用)',
        dataIndex: 'prefix',
        width: 220,
        render: (v: string) => (
          <Text className="mono" style={{ fontSize: 13 }}>{v}…</Text>
        ),
      },
      { title: '备注', dataIndex: 'note', ellipsis: true, render: (v: string) => (v ? <Text style={{ fontSize: 13 }}>{v}</Text> : <Text type="secondary">—</Text>) },
      { title: '创建时间', dataIndex: 'created_at', width: 170, render: (v: string) => <Text style={{ fontSize: 12 }}>{fmtTime(v)}</Text> },
      {
        title: '状态',
        dataIndex: 'revoked',
        width: 100,
        render: (v: boolean) => (v ? <Tag color="red">已吊销</Tag> : <Tag color="green">激活</Tag>),
      },
      {
        title: '操作',
        key: 'actions',
        width: 120,
        render: (_, r) =>
          r.revoked ? (
            <Text type="secondary" style={{ fontSize: 12 }}>吊销于 {fmtTime(r.revoked_at)}</Text>
          ) : (
            <Popconfirm
              title={`吊销「${r.name}」?`}
              description="该 key 将立即无法访问任何 /v1/* 模型端点,不可恢复。"
              okText="吊销"
              okButtonProps={{ danger: true }}
              cancelText="取消"
              onConfirm={() => void revoke(r)}
            >
              <Button type="link" size="small" danger>
                吊销
              </Button>
            </Popconfirm>
          ),
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [revoke],
  )

  return (
    <Flex vertical gap={16}>
      <Flex justify="space-between" align="flex-start">
        <div>
          <Text strong style={{ fontSize: 18 }}>
            API Keys
          </Text>
          <br />
          <Text type="secondary">
            模型面密钥:一个 key 解锁网关内所有模型与协议翻译,只能访问 /v1/*(管理端仍用登录 key)。
          </Text>
        </div>
        <Space>
          <Button icon={<ReloadOutlined spin={loading} />} onClick={() => void load()}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setOpen(true)}>
            生成 Key
          </Button>
        </Space>
      </Flex>

      {lastSecret && (
        <Alert
          type="warning"
          showIcon
          closable
          onClose={() => setLastSecret(null)}
          message={`已生成「${lastSecret.name}」—— 明文只显示这一次`}
          description={
            <Flex vertical gap={4}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                关掉此提示后网关内将无处可查;请立即复制并妥善保存。DB 只存 sha256。
              </Text>
              <Text className="mono" copyable={{ text: lastSecret.secret }} style={{ wordBreak: 'break-all' }}>
                {lastSecret.secret}
              </Text>
            </Flex>
          }
        />
      )}

      {err ? (
        <Card>
          <Result status="warning" title="Key 列表加载失败" subTitle={err} extra={<Button type="primary" onClick={() => void load()}>重试</Button>} />
        </Card>
      ) : (
        <Table<ApiKeyRow>
          rowKey="name"
          columns={columns}
          dataSource={rows ?? []}
          loading={loading && rows == null}
          pagination={false}
          size="middle"
          scroll={{ x: 900 }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有模型面 key,点右上角生成" /> }}
        />
      )}

      <Modal
        title="生成模型面 API Key"
        open={open}
        onOk={() => void submit()}
        onCancel={() => setOpen(false)}
        confirmLoading={saving}
        okText="生成"
        cancelText="取消"
        width={440}
      >
        <Form form={form} layout="vertical" style={{ marginTop: 12 }}>
          <Form.Item
            name="name"
            label="名称"
            rules={[
              { required: true, message: '请输入名称' },
              { pattern: /^[a-zA-Z0-9._-]{1,64}$/, message: '仅限字母/数字/._-,最长 64' },
            ]}
            extra="用于区分用途(如 claude-code、ci),创建后不可改"
          >
            <Input placeholder="claude-code" />
          </Form.Item>
          <Form.Item name="note" label="备注(可选)">
            <Input.TextArea rows={2} placeholder="这台机器 / 这个项目用" maxLength={200} />
          </Form.Item>
        </Form>
      </Modal>
    </Flex>
  )
}
