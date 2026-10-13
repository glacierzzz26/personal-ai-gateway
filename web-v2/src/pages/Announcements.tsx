import { useState } from 'react';
import { App, Button, DatePicker, Dropdown, Form, Input, Modal, Select, Space, Switch, Table, Tooltip } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { MoreOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs, { type Dayjs } from 'dayjs';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import { EmptyState, ErrorState } from '@/components/States';
import { api } from '@/services/api';
import { fmt, TONE_COLOR } from '@/utils/format';
import type { Tone } from '@/utils/format';
import type { AnnouncementDraft, AnnouncementItem, AnnouncementLevel } from '@/types';

const errMsg = (e: unknown) => (e instanceof Error ? e.message : '请稍后重试');

const LEVEL_META: Record<AnnouncementLevel, { tone: Tone; label: string }> = {
  info: { tone: 'aux', label: '通知' },
  warn: { tone: 'warn', label: '注意' },
  danger: { tone: 'err', label: '重要' },
};

/** 级别徽章:色点 + 文字(不靠颜色单独表意)。 */
function LevelBadge({ level }: { level: AnnouncementLevel }) {
  const m = LEVEL_META[level] ?? LEVEL_META.info;
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, color: TONE_COLOR[m.tone] }}>
      <i className={`gw-dot ${m.tone}`} />
      {m.label}
    </span>
  );
}

/** 生效态:停用 / 已过期 / 定时未到 / 生效中(与后端 PendingAnnouncement 同一判定口径)。 */
function statusOf(a: AnnouncementItem): { tone: Tone; label: string } {
  const now = dayjs();
  if (!a.enabled) return { tone: 'aux', label: '已停用' };
  if (a.expiresAt && dayjs(a.expiresAt).isBefore(now)) return { tone: 'aux', label: '已过期' };
  if (a.publishAt && dayjs(a.publishAt).isAfter(now)) return { tone: 'warn', label: '定时未到' };
  return { tone: 'ok', label: '生效中' };
}

interface FormValues {
  title: string;
  body: string;
  level: AnnouncementLevel;
  enabled: boolean;
  publishAt?: Dayjs | null;
  expiresAt?: Dayjs | null;
}

/** 新建/编辑公告弹窗。 */
function AnnouncementModal(props: {
  initial: AnnouncementItem | null;
  open: boolean;
  onCancel: () => void;
  onSubmit: (v: AnnouncementDraft) => Promise<void>;
}) {
  const { initial, open, onCancel, onSubmit } = props;
  const { message } = App.useApp();
  const [form] = Form.useForm<FormValues>();
  const [saving, setSaving] = useState(false);

  // 新建/编辑共用:initial 非空即编辑。默认值随 initial 水合(key 变化会重挂载)。
  const initialValues: Partial<FormValues> = initial
    ? {
        title: initial.title,
        body: initial.body,
        level: initial.level,
        enabled: initial.enabled,
        publishAt: initial.publishAt ? dayjs(initial.publishAt) : null,
        expiresAt: initial.expiresAt ? dayjs(initial.expiresAt) : null,
      }
    : { level: 'info', enabled: true };

  const submit = async () => {
    const v = await form.validateFields();
    setSaving(true);
    try {
      await onSubmit({
        title: v.title.trim(),
        body: v.body.trim(),
        level: v.level,
        enabled: v.enabled,
        publishAt: v.publishAt ? v.publishAt.toISOString() : null,
        expiresAt: v.expiresAt ? v.expiresAt.toISOString() : null,
      });
      form.resetFields();
    } catch (e) {
      message.error(`保存失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={initial ? '编辑公告' : '发布公告'}
      open={open}
      onCancel={onCancel}
      onOk={submit}
      confirmLoading={saving}
      okText={initial ? '保存' : '发布'}
      cancelText="取消"
      destroyOnHidden
      width={560}
    >
      <Form
        form={form}
        layout="vertical"
        requiredMark={false}
        preserve={false}
        initialValues={initialValues}
      >
        <Form.Item name="title" label="标题" rules={[{ required: true, whitespace: true, message: '请输入标题' }]}>
          <Input placeholder="例如:系统维护通知" maxLength={120} />
        </Form.Item>
        <Form.Item name="body" label="正文" rules={[{ required: true, whitespace: true, message: '请输入正文' }]}>
          <Input.TextArea rows={5} placeholder="公告内容，支持换行" maxLength={2000} showCount />
        </Form.Item>
        <Space size={16} style={{ display: 'flex' }} align="start">
          <Form.Item name="level" label="级别" style={{ width: 160 }}>
            <Select
              options={[
                { value: 'info', label: '通知' },
                { value: 'warn', label: '注意' },
                { value: 'danger', label: '重要' },
              ]}
            />
          </Form.Item>
          <Form.Item name="enabled" label="启用" valuePropName="checked" extra="停用后立即不再对任何用户弹出">
            <Switch />
          </Form.Item>
        </Space>
        <Space size={16} style={{ display: 'flex' }} align="start">
          <Form.Item name="publishAt" label="定时发布" extra="留空 = 立即发布" style={{ flex: 1 }}>
            <DatePicker showTime style={{ width: '100%' }} placeholder="立即发布" />
          </Form.Item>
          <Form.Item name="expiresAt" label="过期时间" extra="留空 = 永不过期" style={{ flex: 1 }}>
            <DatePicker showTime style={{ width: '100%' }} placeholder="永不过期" />
          </Form.Item>
        </Space>
      </Form>
    </Modal>
  );
}

export default function Announcements() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const [editing, setEditing] = useState<AnnouncementItem | null>(null);
  const [creating, setCreating] = useState(false);

  const { data = [], isLoading, isError, refetch } = useQuery({
    queryKey: ['announcements'],
    queryFn: api.listAnnouncements,
  });
  const refresh = () => qc.invalidateQueries({ queryKey: ['announcements'] });

  const create = async (v: AnnouncementDraft) => {
    await api.createAnnouncement(v);
    message.success('公告已发布');
    setCreating(false);
    refresh();
  };

  const save = async (id: number, v: AnnouncementDraft) => {
    await api.updateAnnouncement(id, v);
    message.success('公告已保存');
    setEditing(null);
    refresh();
  };

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteAnnouncement(id),
    onSuccess: () => { message.success('已删除'); refresh(); },
    onError: e => message.error(`删除失败:${errMsg(e)}`),
  });

  const confirmDelete = (a: AnnouncementItem) => {
    modal.confirm({
      title: `删除公告「${a.title}」?`,
      content: '删除后该公告不再对任何用户弹出，相关「已读」记录一并清除。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => remove.mutateAsync(a.id),
    });
  };

  const columns: ColumnsType<AnnouncementItem> = [
    {
      // 唯一的弹性列:不设 width + ellipsis + Tooltip,吸收剩余宽度,表格不溢出
      title: '标题', dataIndex: 'title', ellipsis: true,
      render: (v: string, r) => (
        <Tooltip title={v}>
          <b style={{ fontWeight: 500, color: 'var(--gw-text)' }}>{v}</b>
          <span style={{ color: 'var(--gw-text-3)', marginLeft: 8 }}>{r.body.slice(0, 24)}</span>
        </Tooltip>
      ),
    },
    { title: '级别', dataIndex: 'level', width: 90, render: v => <LevelBadge level={v} /> },
    { title: '状态', key: 'status', width: 96, render: (_, r) => { const s = statusOf(r); return (
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        <i className={`gw-dot ${s.tone}`} />
        {s.label}
      </span>
    ); } },
    {
      title: '定时发布', dataIndex: 'publishAt', width: 132,
      render: v => v ? <span className="gw-num" style={{ color: 'var(--gw-text-3)' }}>{dayjs(v).format('MM-DD HH:mm')}</span>
        : <span style={{ color: 'var(--gw-text-3)' }}>立即</span>,
    },
    {
      title: '过期', dataIndex: 'expiresAt', width: 132,
      render: v => v ? <span className="gw-num" style={{ color: 'var(--gw-text-3)' }}>{dayjs(v).format('MM-DD HH:mm')}</span>
        : <span style={{ color: 'var(--gw-text-3)' }}>永不</span>,
    },
    {
      title: '已读', key: 'read', width: 96, align: 'right',
      render: (_, r) => (
        <Tooltip title={`${r.readCount ?? 0} / ${r.userTotal ?? 0} 名普通用户已确认`}>
          <span className="gw-num">{fmt.n(r.readCount ?? 0)}/{fmt.n(r.userTotal ?? 0)}</span>
        </Tooltip>
      ),
    },
    {
      title: '操作', align: 'right', width: 72,
      render: (_, r) => (
        <Dropdown
          trigger={['click']}
          menu={{
            items: [
              { key: 'edit', label: '编辑' },
              { type: 'divider' as const },
              { key: 'delete', label: '删除', danger: true },
            ],
            onClick: ({ key, domEvent }) => {
              domEvent.stopPropagation();
              if (key === 'edit') setEditing(r);
              else if (key === 'delete') confirmDelete(r);
            },
          }}
        >
          <Button type="text" size="small" icon={<MoreOutlined />} aria-label={`更多操作 ${r.title}`} />
        </Dropdown>
      ),
    },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="公告管理"
        desc="发布后所有登录用户会在弹窗中看到；点「我已知晓」后该公告不再对其显示"
        extra={<Button type="primary" onClick={() => setCreating(true)}>发布公告</Button>}
      />

      <Blocks>
        <BlockCard>
          {isError ? (
            <div style={{ padding: '18px 20px' }}>
              <ErrorState
                title="公告加载失败"
                desc="无法读取公告列表，登录会话仍然有效。"
                onRetry={() => void refetch()}
              />
            </div>
          ) : (
            <Table<AnnouncementItem>
              rowKey="id"
              size="middle"
              loading={isLoading && data.length === 0}
              dataSource={data}
              columns={columns}
              pagination={false}
              locale={{
                emptyText: (
                  <EmptyState
                    title="还没有公告"
                    desc="发布一条公告，所有登录用户在进入后台时会看到弹窗。"
                    action={<Button size="small" type="primary" onClick={() => setCreating(true)}>发布公告</Button>}
                  />
                ),
              }}
            />
          )}
        </BlockCard>
      </Blocks>

      <AnnouncementModal
        key={editing?.id ?? 'new'}
        initial={editing}
        open={creating || !!editing}
        onCancel={() => { setCreating(false); setEditing(null); }}
        onSubmit={v => (editing ? save(editing.id, v) : create(v))}
      />
    </div>
  );
}
