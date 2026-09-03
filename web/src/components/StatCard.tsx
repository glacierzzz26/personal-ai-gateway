import { Card, Flex, Skeleton, Statistic, Typography, theme } from 'antd'

const { Text } = Typography

export interface StatCardProps {
  title: string
  value?: number | string
  precision?: number
  prefix?: React.ReactNode
  suffix?: React.ReactNode
  icon?: React.ReactNode
  color?: string // 图标底色/文字色
  loading?: boolean
}

export default function StatCard({ title, value, precision, prefix, suffix, icon, color = '#4c5fe0', loading }: StatCardProps) {
  const { token } = theme.useToken()
  return (
    <Card className="stat-card" variant="borderless" styles={{ body: { padding: 20 } }}>
      <Flex justify="space-between" align="center" gap={16}>
        <div style={{ minWidth: 0 }}>
          <Text type="secondary" style={{ fontSize: 13 }}>
            {title}
          </Text>
          {loading ? (
            <Skeleton.Input active size="small" style={{ marginTop: 8, width: 120 }} />
          ) : (
            <Statistic
              value={value ?? 0}
              precision={precision}
              prefix={prefix}
              suffix={suffix}
              valueStyle={{ fontSize: 26, fontWeight: 650, color: token.colorText }}
            />
          )}
        </div>
        {icon && (
          <div
            style={{
              flex: 'none',
              width: 46,
              height: 46,
              borderRadius: 14,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: 22,
              color,
              background: `${color}1f`,
            }}
          >
            {icon}
          </div>
        )}
      </Flex>
    </Card>
  )
}
