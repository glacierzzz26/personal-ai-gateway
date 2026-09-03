import { useState } from 'react'
import { Alert, App as AntdApp, Button, Card, Input, Typography } from 'antd'
import { ApiError } from './api/client'
import { useAuth } from './auth/AuthContext'
import { useTheme } from './theme'

const { Title, Text } = Typography

// 密钥门:统一网关 key 校验通过才进入管理台。密钥只进 sessionStorage(本会话),不落盘。
export default function KeyGate() {
  const { dark } = useTheme()
  const { login } = useAuth()
  const { message } = AntdApp.useApp()
  const [key, setKey] = useState('')
  const [loading, setLoading] = useState(false)

  const submit = async () => {
    const k = key.trim()
    if (!k) {
      message.warning('请先输入网关密钥')
      return
    }
    setLoading(true)
    try {
      await login(k)
    } catch (e) {
      message.error(e instanceof ApiError ? `密钥校验失败:${e.message}` : '无法连接网关,请确认网关已启动、且网络可达')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className={dark ? 'gate gate-dark' : 'gate gate-light'}>
      <Card className="gate-card" bordered={false}>
        <div className="gate-logo">⇌</div>
        <Title level={3} style={{ textAlign: 'center', marginBottom: 4 }}>
          AI 网关 · 管理台
        </Title>
        <Text type="secondary" style={{ display: 'block', textAlign: 'center', marginBottom: 24 }}>
          输入你的统一网关密钥,查看用量、配额与订阅源
        </Text>
        <Input.Password
          size="large"
          autoFocus
          placeholder="统一网关密钥"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          onPressEnter={submit}
          style={{ marginBottom: 12 }}
        />
        <Button type="primary" size="large" block loading={loading} onClick={submit} style={{ marginBottom: 16 }}>
          验证并进入
        </Button>
        <Alert
          type="info"
          showIcon
          message="密钥仅在当前浏览器会话内使用(sessionStorage),刷新页面需重新输入;不会写入磁盘或仓库。"
        />
      </Card>
      <div className="gate-foot">personal-ai-gateway · 前端仅访问本机/同源网关的 /api 管理接口</div>
    </div>
  )
}
