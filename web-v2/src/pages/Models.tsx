import { useMemo, useState } from 'react';
import {
  App, Button, Card, Col, Empty, Input, Row, Segmented, Select, Switch,
  Table, Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import PageHeader from '@/components/PageHeader';
import ModelCard from '@/components/models/ModelCard';
import ModelDrawer from '@/components/models/ModelDrawer';
import CompareModal from '@/components/models/CompareModal';
import { bestPrice } from '@/components/models/ModelCard';
import { api } from '@/services/api';
import { providers } from '@/services/mock/db';
import { CAP_LABEL, fmt } from '@/utils/format';
import type { Capability, ModelCatalogItem } from '@/types';

type SortKey = 'price' | 'latency' | 'hot' | 'ctx';

const CAPS: Capability[] = ['vision', 'function', 'stream', 'reasoning'];

export default function Models() {
  const { message } = App.useApp();
  const qc = useQueryClient();

  const [kw, setKw] = useState('');
  const [provider, setProvider] = useState<string>('');
  const [cap, setCap] = useState<string>('');
  const [ctxRange, setCtxRange] = useState<string>('');
  const [sort, setSort] = useState<SortKey>('price');
  const [onlyOn, setOnlyOn] = useState(false);
  const [view, setView] = useState<string>('plaza');
  const [compare, setCompare] = useState<number[]>([]);
  const [drawerId, setDrawerId] = useState<number | null>(null);
  const [compareOpen, setCompareOpen] = useState(false);

  const { data: models = [], isLoading } = useQuery({ queryKey: ['models'], queryFn: api.getModels });

  const toggleModel = useMutation({
    mutationFn: (v: { id: number; enabled: boolean }) => api.toggleModel(v.id, v.enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['models'] }),
  });

  const list = useMemo(() => {
    const filtered = models.filter(m => {
      if (kw && !m.name.toLowerCase().includes(kw.toLowerCase())) return false;
      if (provider && !m.offers.some(o => o.provider === provider)) return false;
      if (cap && !m.capabilities.includes(cap as Capability)) return false;
      if (ctxRange === 's' && m.contextWindow >= 32000) return false;
      if (ctxRange === 'm' && (m.contextWindow < 32000 || m.contextWindow > 128000)) return false;
      if (ctxRange === 'l' && m.contextWindow <= 128000) return false;
      if (onlyOn && !m.enabled) return false;
      return true;
    });
    return [...filtered].sort((a, b) => {
      if (sort === 'price') return (bestPrice(a)?.inP ?? 9e9) - (bestPrice(b)?.inP ?? 9e9);
      if (sort === 'latency') {
        return Math.min(...a.offers.map(o => o.latencyMs)) - Math.min(...b.offers.map(o => o.latencyMs));
      }
      if (sort === 'hot') return b.todayRequests - a.todayRequests;
      return b.contextWindow - a.contextWindow;
    });
  }, [models, kw, provider, cap, ctxRange, sort, onlyOn]);

  const toggleCompare = (id: number) => {
    setCompare(prev => {
      if (prev.includes(id)) return prev.filter(x => x !== id);
      if (prev.length >= 4) {
        message.warning('最多同时对比 4 个模型');
        return prev;
      }
      return [...prev, id];
    });
  };

  const compareModels = models.filter(m => compare.includes(m.id));

  const tableCols: ColumnsType<ModelCatalogItem> = [
    {
      title: '模型', dataIndex: 'name',
      render: v => <span className="gw-mono">{v}</span>,
    },
    {
      title: '供应商', key: 'providers', align: 'right',
      render: (_, m) => <span className="gw-num">{m.offers.length}</span>,
    },
    {
      title: '上下文', dataIndex: 'contextWindow', align: 'right',
      render: v => <span className="gw-num">{fmt.ctx(v)}</span>,
    },
    {
      title: '最低输入价', key: 'inP', align: 'right',
      render: (_, m) => {
        const p = bestPrice(m);
        return <span className="gw-num" style={{ color: p ? 'var(--gw-primary)' : undefined }}>{p ? fmt.price(p.inP) : '—'}</span>;
      },
    },
    {
      title: '最低输出价', key: 'outP', align: 'right',
      render: (_, m) => {
        const p = bestPrice(m);
        return <span className="gw-num">{p ? fmt.price(p.outP) : '—'}</span>;
      },
    },
    {
      title: '能力', dataIndex: 'capabilities',
      render: v => (v.length ? v.map((x: Capability) => CAP_LABEL[x]).join(' · ') : '—'),
    },
    {
      title: '今日调用', dataIndex: 'todayRequests', align: 'right',
      render: v => <span className="gw-num">{fmt.k(v)}</span>,
    },
    {
      title: '启用', dataIndex: 'enabled', align: 'center',
      render: (v, m) => (
        <Switch
          size="small"
          checked={v}
          onChange={next => toggleModel.mutate({ id: m.id, enabled: next })}
        />
      ),
    },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="模型广场"
        desc="以模型为中心聚合多家供应商供给源，可对比价格与延迟、编排优先级"
        extra={
          <>
            <Button>从渠道同步</Button>
            <Button type="primary">新增模型</Button>
          </>
        }
      />

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center', marginBottom: 16 }}>
        <Input.Search
          allowClear
          placeholder="搜索模型名"
          style={{ width: 220 }}
          value={kw}
          onChange={e => setKw(e.target.value)}
        />
        <Select
          style={{ width: 140 }} value={provider} onChange={setProvider}
          options={[{ value: '', label: '全部供应商' }, ...providers.map(p => ({ value: p, label: p }))]}
        />
        <Select
          style={{ width: 130 }} value={cap} onChange={setCap}
          options={[{ value: '', label: '全部能力' }, ...CAPS.map(x => ({ value: x, label: CAP_LABEL[x] }))]}
        />
        <Select
          style={{ width: 140 }} value={ctxRange} onChange={setCtxRange}
          options={[
            { value: '', label: '上下文不限' },
            { value: 's', label: '< 32K' },
            { value: 'm', label: '32K – 128K' },
            { value: 'l', label: '> 128K' },
          ]}
        />
        <Select
          style={{ width: 130 }} value={sort} onChange={setSort}
          options={[
            { value: 'price', label: '按最低价' },
            { value: 'latency', label: '按最低延迟' },
            { value: 'hot', label: '按调用量' },
            { value: 'ctx', label: '按上下文' },
          ]}
        />
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--gw-text-2)' }}>
          <Switch size="small" checked={onlyOn} onChange={setOnlyOn} />仅看已启用
        </span>
        <div style={{ marginLeft: 'auto' }}>
          <Segmented
            value={view} onChange={setView}
            options={[{ value: 'plaza', label: '广场' }, { value: 'table', label: '表格' }]}
          />
        </div>
      </div>

      {list.length === 0 ? (
        <Card>
          <Empty description="没有匹配的模型">
            <Button
              onClick={() => { setKw(''); setProvider(''); setCap(''); setCtxRange(''); setOnlyOn(false); }}
            >
              清除筛选
            </Button>
          </Empty>
        </Card>
      ) : view === 'plaza' ? (
        <Row gutter={[16, 16]}>
          {list.map(m => (
            <Col key={m.id} xs={24} sm={12} lg={8} xxl={6}>
              <ModelCard
                model={m}
                picked={compare.includes(m.id)}
                onOpen={() => setDrawerId(m.id)}
                onToggleCompare={() => toggleCompare(m.id)}
                onToggleEnabled={next => toggleModel.mutate({ id: m.id, enabled: next })}
              />
            </Col>
          ))}
        </Row>
      ) : (
        <Card>
          <Table<ModelCatalogItem>
            rowKey="id"
            size="middle"
            loading={isLoading}
            dataSource={list}
            columns={tableCols}
            pagination={{ pageSize: 20, showSizeChanger: false }}
            onRow={r => ({ onClick: () => setDrawerId(r.id), style: { cursor: 'pointer' } })}
          />
        </Card>
      )}

      <div style={{ fontSize: 13, color: 'var(--gw-text-3)', padding: '16px 0' }}>
        共 {list.length} 个模型
      </div>

      {compare.length > 0 && (
        <div
          style={{
            position: 'sticky', bottom: 8, marginTop: 8, zIndex: 30,
            background: 'var(--gw-card)', border: '1px solid var(--gw-border)',
            borderRadius: 10, padding: '10px 14px',
            display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap',
          }}
        >
          <span style={{ fontSize: 13, color: 'var(--gw-text-2)' }}>
            已选 <b>{compare.length}</b> 个
          </span>
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
            {compareModels.map(m => (
              <span
                key={m.id}
                style={{
                  display: 'inline-flex', alignItems: 'center', gap: 6, height: 26, padding: '0 8px',
                  border: '1px solid var(--gw-border)', borderRadius: 6, fontSize: 12,
                  color: 'var(--gw-text-2)',
                }}
              >
                {m.name}
                <Typography.Link onClick={() => toggleCompare(m.id)}>✕</Typography.Link>
              </span>
            ))}
          </div>
          <Button size="small" onClick={() => setCompare([])}>清空</Button>
          <Button size="small" type="primary" onClick={() => setCompareOpen(true)}>开始对比</Button>
        </div>
      )}

      <ModelDrawer
        model={models.find(m => m.id === drawerId) ?? null}
        onClose={() => setDrawerId(null)}
      />
      <CompareModal open={compareOpen} models={compareModels} onClose={() => setCompareOpen(false)} />
    </div>
  );
}
