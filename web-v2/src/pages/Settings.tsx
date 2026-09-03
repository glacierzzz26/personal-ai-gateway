import { useState, type ReactNode } from 'react';
import { App, Button, Card, Col, Input, Row, Segmented, Select, Switch } from 'antd';
import PageHeader from '@/components/PageHeader';
import { useUi } from '@/stores/ui';

interface Item {
  t: string;
  d: string;
  control: ReactNode;
}
interface Group {
  id: string;
  title: string;
  items: Item[];
}

export default function Settings() {
  const { message } = App.useApp();
  const theme = useUi(s => s.theme);
  const setTheme = useUi(s => s.setTheme);

  const [groups] = useState<Group[]>([
    {
      id: 'general', title: '通用', items: [
        { t: '界面语言', d: '当前：简体中文', control: <Select style={{ width: 140 }} defaultValue="zh" options={[{ value: 'zh', label: '简体中文' }, { value: 'en', label: 'English' }]} /> },
        { t: '时区', d: '影响日志与统计的时间口径', control: <Select style={{ width: 200 }} defaultValue="sh" options={[{ value: 'sh', label: 'Asia/Shanghai (UTC+8)' }, { value: 'utc', label: 'UTC' }]} /> },
        { t: '每页条数', d: '列表默认分页大小', control: <Select style={{ width: 100 }} defaultValue={20} options={[20, 50, 100].map(v => ({ value: v, label: String(v) }))} /> },
      ],
    },
    {
      id: 'appearance', title: '外观', items: [
        {
          t: '主题', d: '可选亮色、暗色或跟随系统',
          control: (
            <Segmented
              value={theme}
              onChange={v => setTheme(v as 'light' | 'dark')}
              options={[{ value: 'light', label: '亮色' }, { value: 'dark', label: '暗色' }]}
            />
          ),
        },
        { t: '界面密度', d: '紧凑模式会压缩表格行高', control: <Segmented defaultValue="default" options={[{ value: 'default', label: '标准' }, { value: 'compact', label: '紧凑' }]} /> },
        { t: '侧栏默认折叠', d: '进入时收起左侧菜单', control: <Switch /> },
      ],
    },
    {
      id: 'gateway', title: '网关', items: [
        { t: '请求超时', d: '上游无响应时中断并降级', control: <Input style={{ width: 120 }} defaultValue="60000" suffix="ms" /> },
        { t: '最大重试', d: '同一请求切换供给源的最大次数', control: <Input style={{ width: 100 }} defaultValue="2" suffix="次" /> },
        { t: '失败自动降级', d: '上游报错时切到下一供给源', control: <Switch defaultChecked /> },
      ],
    },
    {
      id: 'network', title: '网络', items: [
        { t: 'HTTP 代理', d: '留空则直连', control: <Input style={{ width: 240 }} placeholder="http://127.0.0.1:7890" /> },
        { t: '跳过 TLS 校验', d: '仅在自签证书环境开启，存在中间人风险', control: <Switch /> },
      ],
    },
    {
      id: 'logs', title: '日志', items: [
        { t: '保留天数', d: '超期日志自动清理', control: <Select style={{ width: 120 }} defaultValue={7} options={[7, 30, 90].map(v => ({ value: v, label: `${v} 天` }))} /> },
        { t: '记录请求体', d: '关闭后仅记录元数据，节省空间', control: <Switch defaultChecked /> },
        { t: '采样率', d: '高流量时可按比例采样', control: <Input style={{ width: 100 }} defaultValue="100" suffix="%" /> },
      ],
    },
    {
      id: 'about', title: '关于', items: [
        { t: '版本', d: 'v0.8.2 · 构建于 2026-09-02', control: <Button size="small" onClick={() => message.info('已是最新版本')}>检查更新</Button> },
        { t: '清空全部日志', d: '不可恢复', control: <Button size="small" danger>清空</Button> },
      ],
    },
  ]);

  const [active, setActive] = useState('general');

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
            <Card key={g.id} id={g.id} title={g.title} style={{ marginBottom: 16 }} styles={{ body: { padding: 0 } }}>
              {g.items.map(it => (
                <div
                  key={it.t}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 16, padding: '12px 20px',
                    borderBottom: '1px solid var(--gw-border-2)',
                  }}
                >
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 14 }}>{it.t}</div>
                    <div style={{ fontSize: 12, color: 'var(--gw-text-3)', marginTop: 2 }}>{it.d}</div>
                  </div>
                  <div style={{ flex: '0 0 auto' }}>{it.control}</div>
                </div>
              ))}
              <div style={{ padding: '12px 20px', display: 'flex', justifyContent: 'flex-end' }}>
                <Button size="small" onClick={() => message.success(`${g.title} 已保存`)}>保存</Button>
              </div>
            </Card>
          ))}
        </Col>
      </Row>
    </div>
  );
}
