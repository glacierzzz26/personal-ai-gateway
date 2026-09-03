import { Card, Flex, Typography, theme } from 'antd'
import { CheckCircleFilled, CloseCircleFilled, InfoCircleFilled, WarningFilled } from '@ant-design/icons'

const { Text } = Typography

export type Severity = 'error' | 'warning' | 'success' | 'info'

export interface AlertCardProps {
  severity: Severity
  title: React.ReactNode
  description?: React.ReactNode
  extra?: React.ReactNode
}

const ICONS: Record<Severity, React.ReactNode> = {
  error: <CloseCircleFilled />,
  warning: <WarningFilled />,
  success: <CheckCircleFilled />,
  info: <InfoCircleFilled />,
}

// ★ 告警/异常一律用 antd 卡片承载:按严重度着色左侧图标与标题,右侧可挂操作(extra)。
export default function AlertCard({ severity, title, description, extra }: AlertCardProps) {
  const { token } = theme.useToken()
  const color =
    severity === 'error' ? token.colorError : severity === 'warning' ? token.colorWarning : severity === 'success' ? token.colorSuccess : token.colorInfo
  return (
    <Card
      size="small"
      className="alert-card"
      style={{ borderColor: `${color}66`, background: token.colorBgContainer }}
      styles={{ body: { padding: '12px 16px' } }}
    >
      <Flex gap={12} align="flex-start">
        <span style={{ color, fontSize: 18, lineHeight: '24px', marginTop: 1 }}>{ICONS[severity]}</span>
        <Flex vertical gap={2} style={{ flex: 1, minWidth: 0 }}>
          <Text strong style={{ color }}>{title}</Text>
          {description && <Text type="secondary" style={{ fontSize: 13, whiteSpace: 'pre-wrap' }}>{description}</Text>}
        </Flex>
        {extra && <div style={{ flex: 'none', paddingTop: 2 }}>{extra}</div>}
      </Flex>
    </Card>
  )
}
