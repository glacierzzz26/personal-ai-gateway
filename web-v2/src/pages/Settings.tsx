import { useEffect, useState, type ReactNode } from 'react';
import { Alert, App, Button, Card, Col, Input, InputNumber, Row, Segmented, Select, Skeleton, Slider, Switch, Typography } from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import PageHeader from '@/components/PageHeader';
import { api } from '@/services/api';
import { useUi } from '@/stores/ui';
import type { Settings as SettingsModel } from '@/types';

/** 时区选择:分钟偏移 东为正。仅列常见值,默认 Asia/Shanghai(+480)。 */
const TZ_OPTIONS = [
  { value: -480, label: 'UTC-8 洛杉矶' },
  { value: -420, label: 'UTC-7 丹佛' },
  { value: -300, label: 'UTC-5 纽约' },
  { value: 0, label: 'UTC+0 伦敦' },
  { value: 60, label: 'UTC+1 柏林 / 巴黎' },
  { value: 180, label: 'UTC+3 莫斯科' },
  { value: 480, label: 'UTC+8 北京时间' },
  { value: 540, label: 'UTC+9 东京 / 首尔' },
  { value: 600, label: 'UTC+10 悉尼' },
];

interface RowDef {
  key: string;
  t: string;
  d: string;
  render: (form: SettingsModel, set: <K extends keyof SettingsModel>(k: K, v: SettingsModel[K]) => void) => ReactNode;
}
interface Group {
  id: string;
  title: string;
  note?: string;
  /** 是否把改动写回网关(否则为纯本机偏好) */
  persisted?: boolean;
  rows: RowDef[];
}

export default function Settings() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const theme = useUi(s => s.theme);
  const setTheme = useUi(s => s.setTheme);

  const { data: server, isLoading, error } = useQuery({ queryKey: ['settings'], queryFn: api.getSettings });
  const [form, setForm] = useState<SettingsModel | null>(null);
  const [saving, setSaving] = useState(false);
  const [clearing, setClearing] = useState(false);

  // 数据就绪后初始化本地编辑副本(本次挂载只初始化一次,避免编辑被后台刷新覆盖)。
  useEffect(() => {
    if (server) setForm(f => f ?? server);
  }, [server]);

  const set = <K extends keyof SettingsModel>(k: K, v: SettingsModel[K]) =>
    setForm(f => (f ? { ...f, [k]: v } : f));

  const save = async () => {
    if (!form) return;
    setSaving(true);
    try {
      const saved = await api.updateSettings(form);
      setForm(saved);
      qc.setQueryData(['settings'], saved);
      message.success('设置已保存');
    } catch (e) {
      message.error(e instanceof Error ? e.message : '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const clearLogs = () => {
    modal.confirm({
      title: '清空全部请求日志?',
      content: '所有日志与用量统计将被删除,此操作不可恢复。',
      okText: '清空', okButtonProps: { danger: true }, cancelText: '取消',
      onOk: async () => {
        setClearing(true);
        try {
          await api.clearLogs();
          message.success('日志已清空');
          await qc.invalidateQueries({ queryKey: ['logs'] });
          await qc.invalidateQueries({ queryKey: ['recentLogs'] });
          await qc.invalidateQueries({ queryKey: ['overview'] });
          await qc.invalidateQueries({ queryKey: ['usage'] });
        } catch (e) {
          message.error(e instanceof Error ? e.message : '清空失败');
        } finally {
          setClearing(false);
        }
      },
    });
  };

  const groups: Group[] = [
    {
      id: 'gateway', title: '网关', persisted: true, note: '改动保存后对后续请求即时生效',
      rows: [
        {
          key: 'timeout', t: '请求超时', d: '上游无响应时中断并切换供给源',
          render: (f, s) => (
            <InputNumber style={{ width: 140 }} min={1000} step={1000} value={f.requestTimeoutMs}
              addonAfter="ms" onChange={v => s('requestTimeoutMs', Number(v) || 60000)} />
          ),
        },
        {
          key: 'retries', t: '最大重试', d: '同一请求切换供给源的最大次数',
          render: (f, s) => (
            <InputNumber style={{ width: 120 }} min={0} max={10} value={f.maxRetries}
              addonAfter="次" onChange={v => s('maxRetries', Number(v) || 0)} />
          ),
        },
        {
          key: 'degrade', t: '失败自动降级', d: '上游报错或超时时自动尝试下一个供给源',
          render: (f, s) => (
            <Switch checked={f.degradeOnError} onChange={v => s('degradeOnError', v)} />
          ),
        },
      ],
    },
    {
      id: 'network', title: '网络', persisted: true,
      rows: [
        {
          key: 'proxy', t: 'HTTP 代理', d: '出站请求经代理转发,留空则直连',
          render: (f, s) => (
            <Input style={{ width: 260 }} placeholder="http://127.0.0.1:7890"
              value={f.httpProxy ?? ''} onChange={e => s('httpProxy', e.target.value)} />
          ),
        },
        {
          key: 'tls', t: '跳过 TLS 校验', d: '仅在自签证书环境开启,存在中间人风险',
          render: (f, s) => (
            <Switch checked={f.skipTlsVerify} onChange={v => s('skipTlsVerify', v)} />
          ),
        },
      ],
    },
    {
      id: 'logs', title: '日志', persisted: true,
      rows: [
        {
          key: 'retention', t: '保留天数', d: '超期日志自动清理,0 = 不清理',
          render: (f, s) => (
            <Select style={{ width: 150 }} value={f.logRetentionDays}
              onChange={v => s('logRetentionDays', v)}
              options={[{ value: 0, label: '不自动清理' }, { value: 7, label: '7 天' }, { value: 30, label: '30 天' }, { value: 90, label: '90 天' }]} />
          ),
        },
        {
          key: 'body', t: '记录请求体', d: '关闭后仅记录元数据(模型/Token/耗时/费用),节省空间',
          render: (f, s) => (
            <Switch checked={f.recordRequestBody} onChange={v => s('recordRequestBody', v)} />
          ),
        },
        {
          key: 'sample', t: '采样率', d: '个人规模建议 100;高流量时可降低采样',
          render: (f, s) => (
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, width: 280 }}>
              <Slider style={{ flex: 1 }} min={0} max={100} value={f.sampleRatePct}
                onChange={v => s('sampleRatePct', v)} />
              <span className="gw-num" style={{ width: 34, textAlign: 'right' }}>{f.sampleRatePct}%</span>
            </div>
          ),
        },
      ],
    },
    {
      id: 'general', title: '通用', persisted: true,
      rows: [
        {
          key: 'tz', t: '时区', d: '影响日志时间与统计分桶的本地口径',
          render: (f, s) => (
            <Select style={{ width: 260 }} value={f.tzOffsetMin}
              onChange={v => s('tzOffsetMin', v)} options={TZ_OPTIONS} />
          ),
        },
        {
          key: 'publicBaseUrl', t: '对外基址', d: '生成 Claude 配置时的网关地址(如 https://ai-gateway.lan);留空则按访问地址推断',
          render: (f, s) => (
            <Input style={{ width: 320 }} placeholder="留空 = 按访问地址推断"
              value={f.publicBaseUrl ?? ''} onChange={e => s('publicBaseUrl', e.target.value)} />
          ),
        },
      ],
    },
    {
      id: 'appearance', title: '外观(仅本机显示)', note: '主题偏好保存在当前浏览器,不写入网关',
      rows: [
        {
          key: 'theme', t: '主题', d: '亮色 / 暗色,立即生效',
          render: () => (
            <Segmented value={theme} onChange={v => setTheme(v as 'light' | 'dark')}
              options={[{ value: 'light', label: '亮色' }, { value: 'dark', label: '暗色' }]} />
          ),
        },
      ],
    },
    {
      id: 'about', title: '关于',
      rows: [
        {
          key: 'version', t: '版本', d: '个人 AI 网关 · 管理面 v2(gateway-v2.db)',
          render: () => <Typography.Text type="secondary">同仓演进重构版</Typography.Text>,
        },
        {
          key: 'clear', t: '清空全部日志', d: '不可恢复,请先导出需要保留的记录',
          render: () => (
            <Button size="small" danger loading={clearing} onClick={clearLogs}>清空</Button>
          ),
        },
      ],
    },
  ];

  const [active, setActive] = useState('gateway');

  if (!form) {
    return (
      <div className="gw-page">
        <PageHeader title="系统设置" desc="网关运行参数与界面偏好" />
        <Card>
          {isLoading ? (
            <Skeleton active paragraph={{ rows: 8 }} />
          ) : (
            <Alert type="error" showIcon message="设置加载失败" description={error instanceof Error ? error.message : '请稍后重试'} />
          )}
        </Card>
      </div>
    );
  }

  return (
    <div className="gw-page">
      <PageHeader title="系统设置" desc="网关运行参数与界面偏好" />

      <Row gutter={20}>
        <Col flex="0 0 160px" style={{ display: window.innerWidth > 900 ? 'block' : 'none' }}>
          <div style={{ position: 'sticky', top: 24 }}>
            {groups.map(g => (
              <a
                key={g.id}
                onClick={() => {
                  setActive(g.id);
                  document.getElementById(g.id)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
                }}
                style={{
                  display: 'block', fontSize: 13, padding: '6px 0 6px 12px', cursor: 'pointer',
                  borderLeft: `2px solid ${active === g.id ? 'var(--gw-primary)' : 'var(--gw-border)'}`,
                  color: active === g.id ? 'var(--gw-primary)' : 'var(--gw-text-2)',
                }}
              >
                {g.title}
              </a>
            ))}
          </div>
        </Col>

        <Col flex="1 1 auto" style={{ minWidth: 0 }}>
          {groups.map(g => (
            <Card
              key={g.id} id={g.id} title={g.title}
              style={{ marginBottom: 16 }} styles={{ body: { padding: 0 } }}
              extra={g.persisted ? undefined : <span style={{ fontSize: 12, color: 'var(--gw-text-3)', fontWeight: 400 }}>{g.note}</span>}
            >
              {g.rows.map(r => (
                <div
                  key={r.key}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 16, padding: '12px 20px',
                    borderBottom: '1px solid var(--gw-border-2)',
                  }}
                >
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 14 }}>{r.t}</div>
                    <div style={{ fontSize: 12, color: 'var(--gw-text-3)', marginTop: 2 }}>{r.d}</div>
                  </div>
                  <div style={{ flex: '0 0 auto' }}>{r.render(form!, set)}</div>
                </div>
              ))}
              {g.persisted && (
                <div style={{ padding: '12px 20px', display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: 12 }}>
                  <span style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>{g.note}</span>
                  <Button size="small" type="primary" ghost loading={saving} onClick={save}>保存</Button>
                </div>
              )}
            </Card>
          ))}
        </Col>
      </Row>
    </div>
  );
}
