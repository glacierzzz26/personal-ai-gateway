import { useEffect, useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Block as BlockCard, BlockHead, BlockBody, Blocks } from '@/components/Block';
import { DegradedNote, ErrorState, SkLines } from '@/components/States';
import { Btn, Input, PageHead, Select, Switch } from '@/ui/primitives';
import { confirm } from '@/ui/overlay';
import { toast } from '@/ui/toast';
import { api } from '@/services/api';
import type { PriceCurrency, Settings as SettingsModel } from '@/types';

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
  /** 组内说明，显示在标题右侧 */
  note?: string;
  /** 是否把改动写回网关(否则为只读信息) */
  persisted?: boolean;
  /** 组右侧的危险操作 */
  danger?: { label: string; text: string; run: () => void; loading: boolean };
  rows: RowDef[];
}

/** 数值输入 + 尾缀单位（InputNumber addonAfter 的同形替身） */
function NumInput(p: { value: number; min?: number; max?: number; step?: number; unit: string; placeholder?: string; onChange: (v: number) => void }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      <Input type="number" min={p.min} max={p.max} step={p.step} placeholder={p.placeholder}
        value={p.value} onChange={e => p.onChange(Number(e.target.value) || 0)} />
      <span className="cell-sub" style={{ flex: '0 0 auto' }}>{p.unit}</span>
    </div>
  );
}

export default function Settings() {
  const qc = useQueryClient();

  const { data: server, isLoading, error, refetch } = useQuery({ queryKey: ['settings'], queryFn: api.getSettings });
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
      toast.ok('设置已保存');
    } catch (e) {
      toast.err(e instanceof Error ? e.message : '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const clearLogs = () => {
    confirm({
      title: '清空全部请求日志?',
      body: '所有日志与用量统计将被删除,此操作不可恢复。',
      okText: '清空', danger: true, cancelText: '取消',
    }).then(async ok => {
      if (!ok) return;
      setClearing(true);
      try {
        await api.clearLogs();
        toast.ok('日志已清空');
        await qc.invalidateQueries({ queryKey: ['logs'] });
        await qc.invalidateQueries({ queryKey: ['recentLogs'] });
        await qc.invalidateQueries({ queryKey: ['overview'] });
        await qc.invalidateQueries({ queryKey: ['usage'] });
      } catch (e) {
        toast.err(e instanceof Error ? e.message : '清空失败');
      } finally {
        setClearing(false);
      }
    });
  };

  const groups: Group[] = [
    {
      id: 'gateway', title: '网关', persisted: true, note: '保存后对后续请求即时生效',
      rows: [
        {
          key: 'timeout', t: '请求超时', d: '上游在该窗口内无响应即中断并尝试下一个供给源；应留出上游最慢一次生成的时间',
          render: (f, s) => (
            <NumInput unit="ms" min={1000} step={1000} value={f.requestTimeoutMs}
              onChange={v => s('requestTimeoutMs', v || 90000)} />
          ),
        },
        {
          key: 'retries', t: '最大重试', d: '同一请求切换供给源的最大次数',
          render: (f, s) => (
            <NumInput unit="次" min={0} max={10} value={f.maxRetries}
              onChange={v => s('maxRetries', v)} />
          ),
        },
        {
          key: 'degrade', t: '失败自动降级', d: '上游报错或超时时自动尝试下一个供给源',
          render: (f, s) => <Switch checked={f.degradeOnError} onChange={v => s('degradeOnError', v)} />,
        },
      ],
    },
    {
      id: 'network', title: '网络', persisted: true,
      rows: [
        {
          key: 'proxy', t: 'HTTP 代理', d: '出站请求经代理转发,留空则直连',
          render: (f, s) => (
            <Input style={{ width: 280 }} placeholder="http://127.0.0.1:7890"
              value={f.httpProxy ?? ''} onChange={e => s('httpProxy', e.target.value)} />
          ),
        },
        {
          key: 'tls', t: '跳过 TLS 校验', d: '仅在自签证书环境开启,存在中间人风险(域名公信证书环境无需开启)',
          render: (f, s) => <Switch checked={f.skipTlsVerify} onChange={v => s('skipTlsVerify', v)} />,
        },
      ],
    },
    {
      id: 'logs', title: '日志', persisted: true,
      rows: [
        {
          key: 'retention', t: '保留天数', d: '超期日志自动清理,0 = 不清理',
          render: (f, s) => (
            <Select style={{ width: 170 }} value={String(f.logRetentionDays)}
              onChange={e => s('logRetentionDays', Number(e.target.value))}>
              <option value="0">不自动清理</option>
              <option value="7">7 天</option>
              <option value="30">30 天</option>
              <option value="90">90 天</option>
            </Select>
          ),
        },
        {
          key: 'body', t: '记录请求体', d: '关闭后仅记录元数据(模型 / Token / 耗时 / 费用),节省空间',
          render: (f, s) => <Switch checked={f.recordRequestBody} onChange={v => s('recordRequestBody', v)} />,
        },
        {
          key: 'sample', t: '采样率', d: '个人规模建议 100;高流量时可降低采样以省空间',
          render: (f, s) => (
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <input
                type="range" min={0} max={100} value={f.sampleRatePct}
                style={{ flex: 1, ['--p' as string]: `${f.sampleRatePct}%` } as React.CSSProperties}
                onChange={e => s('sampleRatePct', Number(e.target.value))}
              />
              <span className="num" style={{ width: 46, textAlign: 'right' }}>{f.sampleRatePct}%</span>
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
            <Select style={{ width: 280 }} value={String(f.tzOffsetMin)}
              onChange={e => s('tzOffsetMin', Number(e.target.value))}>
              {TZ_OPTIONS.map(tz => <option key={tz.value} value={String(tz.value)}>{tz.label}</option>)}
            </Select>
          ),
        },
        {
          key: 'publicBaseUrl', t: '对外基址', d: '生成 Claude 配置时的网关地址(如 https://ai-gateway.lan);留空则按访问地址推断',
          render: (f, s) => (
            <Input style={{ width: 320 }} placeholder="留空 = 按访问地址推断"
              value={f.publicBaseUrl ?? ''} onChange={e => s('publicBaseUrl', e.target.value)} />
          ),
        },
        {
          key: 'displayCurrency', t: '计价币种',
          d: '全站价格(模型单价 / 用量花费 / 官方参考价)统一按此币种展示,也是「应用官方价」的目标币种',
          render: (f, s) => (
            <Select style={{ width: 200 }} value={f.displayCurrency ?? 'CNY'}
              onChange={e => s('displayCurrency', e.target.value as PriceCurrency)}>
              <option value="CNY">人民币 ¥</option>
              <option value="USD">美元 $</option>
            </Select>
          ),
        },
        {
          key: 'usdPerCny', t: 'USD/CNY 汇率',
          d: '仅当官方价原币种与计价币种不一致时用于折算(如官方为美元、计价为人民币)。手工维护:1 元人民币合多少美元就填多少(如 0.139);留 0 = 未设,此时拒绝折算,不臆造汇率',
          render: (f, s) => (
            <NumInput
              unit="" min={0} max={10} step={0.001}
              placeholder="0 = 未设"
              value={f.usdPerCny ?? 0}
              onChange={v => s('usdPerCny', v)}
            />
          ),
        },
        {
          key: 'priceMultiplier', t: '全局售价倍率',
          d: '本站价 = 官方价 × 倍率,是展示给用户的售价口径(成本仅管理员可见)。留空或 0 = 1.0 不加价;单个模型可在「模型广场」编辑页单独覆盖',
          render: (f, s) => (
            <NumInput
              unit="" min={0.01} max={1000} step={0.1}
              placeholder="1 = 不加价"
              value={f.priceMultiplier ?? 1}
              onChange={v => s('priceMultiplier', v || 1)}
            />
          ),
        },
      ],
    },
    {
      id: 'data', title: '数据',
      danger: { label: '清空全部日志', text: '不可恢复,请先导出需要保留的记录', run: clearLogs, loading: clearing },
      rows: [
        {
          key: 'db', t: '数据库', d: '日志、渠道、令牌与设置同库存放(gateway-v2.db,SQLite WAL)',
          render: () => <span className="mono" style={{ color: 'var(--t3)' }}>gateway-v2.db</span>,
        },
      ],
    },
  ];

  const [active, setActive] = useState('gateway');

  const goTo = (id: string) => {
    setActive(id);
    document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  if (!form) {
    return (
      <div>
        <PageHead title="系统设置" sub="网关运行参数" />
        <Blocks>
          <BlockCard>
            <div style={{ padding: '18px 20px' }}>
              {isLoading ? (
                <SkLines rows={['w40', 'w80', 'w60', 'w80', 'w60']} />
              ) : (
                <ErrorState
                  title="设置加载失败"
                  desc={error instanceof Error ? error.message : '请稍后重试'}
                  onRetry={() => void refetch()}
                />
              )}
            </div>
          </BlockCard>
        </Blocks>
      </div>
    );
  }

  return (
    <div>
      <PageHead
        title="系统设置"
        sub="网关运行参数；改动保存后对后续请求即时生效"
        actions={
          <Btn variant="primary" loading={saving} onClick={save}>
            保存设置
          </Btn>
        }
      />

      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,168px) minmax(0,1fr)', gap: 24, alignItems: 'start' }}>
        <nav aria-label="设置分组" style={{ position: 'sticky', top: 86, display: 'grid', gap: 2 }}>
          {groups.map(g => (
            <button
              key={g.id}
              type="button"
              aria-current={active === g.id}
              onClick={() => goTo(g.id)}
              style={{
                display: 'block', width: '100%', textAlign: 'left', border: 'none', cursor: 'pointer',
                padding: '7px 10px', borderRadius: 4, fontSize: 12.5, fontFamily: 'inherit',
                background: active === g.id ? 'var(--panel-2)' : 'transparent',
                color: active === g.id ? 'var(--brand)' : 'var(--t2)',
                fontWeight: active === g.id ? 500 : 400,
              }}
            >
              {g.title}
            </button>
          ))}
        </nav>

        <Blocks>
          {groups.map(g => (
            <BlockCard key={g.id} id={g.id}>
              <BlockHead
                title={g.title}
                sub={g.note}
                right={g.danger && (
                  <>
                    <span style={{ fontSize: 13, color: 'var(--t3)' }}>{g.danger.text}</span>
                    <Btn size="sm" variant="danger" loading={g.danger.loading} onClick={g.danger.run}>
                      {g.danger.label}
                    </Btn>
                  </>
                )}
              />

              <BlockBody style={{ padding: '4px 20px 8px' }}>
                {g.rows.map(r => (
                  <div
                    key={r.key}
                    style={{
                      display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,340px)',
                      gap: 16, alignItems: 'center', padding: '13px 0', borderBottom: '1px solid var(--line-2)',
                    }}
                  >
                    <div>
                      <div style={{ fontSize: 13.5, fontWeight: 500, color: 'var(--t1)' }}>{r.t}</div>
                      <div className="cell-sub" style={{ marginTop: 2 }}>{r.d}</div>
                    </div>
                    <div style={{ justifySelf: 'end', width: '100%' }}>{r.render(form, set)}</div>
                  </div>
                ))}
              </BlockBody>

              {g.persisted && (
                <div
                  style={{
                    padding: '12px 20px', borderTop: '1px solid var(--line)',
                    display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: 12,
                  }}
                >
                  <span style={{ fontSize: 13, color: 'var(--t3)' }}>保存后对后续请求即时生效</span>
                  <Btn size="sm" variant="primary" loading={saving} onClick={save}>保存</Btn>
                </div>
              )}
            </BlockCard>
          ))}

          {/* 降级说明：设置改动不回溯已产生的日志，这里明确告知，避免误解 */}
          <DegradedNote title="设置不影响历史数据">
            修改超时、时区或采样率只作用于保存之后的请求；已产生的日志与统计不会被重算。
          </DegradedNote>
        </Blocks>
      </div>
    </div>
  );
}
