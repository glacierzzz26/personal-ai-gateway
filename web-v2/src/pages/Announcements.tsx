import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import { EmptyState, ErrorState, SkLines } from '@/components/States';
import { Badge, Button, Field, Input, Select, Switch, Textarea } from '@/ui/primitives';
import { Menu, Modal, confirm } from '@/ui/overlay';
import { message } from '@/ui/toast';
import { Icon } from '@/ui/icons';
import { api } from '@/services/api';
import { fmt, TONE_COLOR } from '@/utils/format';
import type { Tone } from '@/utils/format';
import type { AnnouncementDraft, AnnouncementItem, AnnouncementLevel } from '@/types';

const errMsg = (e: unknown) => (e instanceof Error ? e.message : '请稍后重试');

/** 级别 → 语义色 + 文字 + 左条 severity 类(.alert-item.sev-*)。 */
const LEVEL_META: Record<AnnouncementLevel, { tone: Tone; label: string; sev: string; badge: 'brand' | 'amber' | 'red' }> = {
  info: { tone: 'aux', label: '通知', sev: 'sev-info', badge: 'brand' },
  warn: { tone: 'warn', label: '注意', sev: 'sev-warn', badge: 'amber' },
  danger: { tone: 'err', label: '重要', sev: 'sev-error', badge: 'red' },
};

/** Tone → 状态圆点类(.dot.g/.a/.r/.s)。 */
const TONE_DOT: Record<Tone, string> = { ok: 'g', warn: 'a', err: 'r', aux: 's' };

/** 生效态:停用 / 已过期 / 定时未到 / 生效中(与后端 PendingAnnouncement 同一判定口径)。 */
function statusOf(a: AnnouncementItem): { tone: Tone; label: string } {
  const now = dayjs();
  if (!a.enabled) return { tone: 'aux', label: '已停用' };
  if (a.expiresAt && dayjs(a.expiresAt).isBefore(now)) return { tone: 'aux', label: '已过期' };
  if (a.publishAt && dayjs(a.publishAt).isAfter(now)) return { tone: 'warn', label: '定时未到' };
  return { tone: 'ok', label: '生效中' };
}

/** ISO ↔ <input type="datetime-local"> 本地串互转(留空 = 未设置)。 */
const toLocalInput = (iso?: string | null): string => (iso ? dayjs(iso).format('YYYY-MM-DDTHH:mm') : '');
const fromLocalInput = (v: string): string | null => (v ? dayjs(v).toISOString() : null);

const fmtTime = (iso?: string | null) => (iso ? dayjs(iso).format('MM-DD HH:mm') : '');

/** 新建 / 编辑公告弹窗(受控,无 antd Form)。initial 非空即编辑。 */
function AnnouncementModal(props: {
  initial: AnnouncementItem | null;
  onCancel: () => void;
  onSubmit: (v: AnnouncementDraft) => Promise<void>;
}) {
  const { initial, onCancel, onSubmit } = props;
  const [title, setTitle] = useState(initial?.title ?? '');
  const [body, setBody] = useState(initial?.body ?? '');
  const [level, setLevel] = useState<AnnouncementLevel>(initial?.level ?? 'info');
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [publishAt, setPublishAt] = useState(toLocalInput(initial?.publishAt));
  const [expiresAt, setExpiresAt] = useState(toLocalInput(initial?.expiresAt));
  const [errs, setErrs] = useState<{ title?: string; body?: string }>({});
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    const next: { title?: string; body?: string } = {};
    if (!title.trim()) next.title = '请输入标题';
    if (!body.trim()) next.body = '请输入正文';
    setErrs(next);
    if (Object.keys(next).length) return;
    setSaving(true);
    try {
      await onSubmit({
        title: title.trim(),
        body: body.trim(),
        level,
        enabled,
        publishAt: fromLocalInput(publishAt),
        expiresAt: fromLocalInput(expiresAt),
      });
    } catch (e) {
      message.error(`保存失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="overlay" style={{ zIndex: 70 }}>
      <Modal
        title={initial ? '编辑公告' : '发布公告'}
        width={560}
        onClose={onCancel}
        footer={
          <>
            <Button variant="secondary" onClick={onCancel}>
              取消
            </Button>
            <Button variant="primary" loading={saving} onClick={() => void submit()}>
              {initial ? '保存' : '发布'}
            </Button>
          </>
        }
      >
        <Field label="标题" error={errs.title}>
          <Input value={title} maxLength={120} placeholder="例如:系统维护通知" onChange={e => setTitle(e.target.value)} />
        </Field>
        <Field label="正文" error={errs.body}>
          <Textarea value={body} rows={5} maxLength={2000} placeholder="公告内容，支持换行" onChange={e => setBody(e.target.value)} />
        </Field>
        <div className="field-row">
          <Field label="级别">
            <Select
              value={level}
              onChange={e => setLevel(e.target.value as AnnouncementLevel)}
              options={[
                { value: 'info', label: '通知' },
                { value: 'warn', label: '注意' },
                { value: 'danger', label: '重要' },
              ]}
            />
          </Field>
          <Field label="启用" hint="停用后立即不再对任何用户弹出">
            <Switch checked={enabled} onChange={setEnabled} />
          </Field>
        </div>
        <div className="field-row">
          <Field label="定时发布" hint="留空 = 立即发布">
            <Input type="datetime-local" value={publishAt} onChange={e => setPublishAt(e.target.value)} />
          </Field>
          <Field label="过期时间" hint="留空 = 永不过期">
            <Input type="datetime-local" value={expiresAt} onChange={e => setExpiresAt(e.target.value)} />
          </Field>
        </div>
      </Modal>
    </div>
  );
}

export default function Announcements() {
  const qc = useQueryClient();
  const [editing, setEditing] = useState<AnnouncementItem | null>(null);
  const [creating, setCreating] = useState(false);
  const [menuId, setMenuId] = useState<number | null>(null);

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
    onSuccess: () => {
      message.success('已删除');
      refresh();
    },
    onError: e => message.error(`删除失败:${errMsg(e)}`),
  });

  const confirmDelete = (a: AnnouncementItem) => {
    void confirm({
      title: `删除公告「${a.title}」?`,
      content: '删除后该公告不再对任何用户弹出，相关「已读」记录一并清除。',
      okText: '删除',
      danger: true,
    }).then(ok => {
      if (ok) remove.mutate(a.id);
    });
  };

  const modalOpen = creating || !!editing;

  return (
    <>
      <PageHeader
        title="公告管理"
        desc="发布后所有登录用户会在弹窗中看到；点「我已知晓」后该公告不再对其显示"
        extra={
          <Button variant="primary" icon="plus" onClick={() => setCreating(true)}>
            发布公告
          </Button>
        }
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
          ) : isLoading && data.length === 0 ? (
            <div style={{ padding: 14 }}>
              <SkLines rows={['w80', 'w60', 'w80']} />
            </div>
          ) : data.length === 0 ? (
            <EmptyState
              title="还没有公告"
              desc="发布一条公告，所有登录用户在进入后台时会看到弹窗。"
              action={
                <Button size="sm" variant="primary" onClick={() => setCreating(true)}>
                  发布公告
                </Button>
              }
            />
          ) : (
            <div className="stack-sm">
              {data.map(a => {
                const meta = LEVEL_META[a.level] ?? LEVEL_META.info;
                const st = statusOf(a);
                return (
                  <div key={a.id} className={`alert-item ${meta.sev}`}>
                    <div className="alert-body">
                      <div className="alert-title">
                        <Badge tone={meta.badge}>{meta.label}</Badge>
                        {a.title}
                      </div>
                      <div className="alert-text">{a.body}</div>
                      <div className="alert-meta">
                        <span className="hstack" style={{ gap: 5, color: TONE_COLOR[st.tone] }}>
                          <i className={`dot ${TONE_DOT[st.tone]}`} />
                          {st.label}
                        </span>
                        <span>{a.publishAt ? `${fmtTime(a.publishAt)} 发布` : '立即发布'}</span>
                        <span>{a.expiresAt ? `${fmtTime(a.expiresAt)} 过期` : '永不过期'}</span>
                        <span className="num">
                          已读 {fmt.n(a.readCount ?? 0)}/{fmt.n(a.userTotal ?? 0)}
                        </span>
                      </div>
                    </div>
                    <div className="rel">
                      <button
                        className="icon-btn"
                        aria-label={`更多操作 ${a.title}`}
                        onClick={() => setMenuId(m => (m === a.id ? null : a.id))}
                      >
                        <Icon name="dots" size={15} />
                      </button>
                      {menuId === a.id && (
                        <Menu
                          onClose={() => setMenuId(null)}
                          items={[
                            { label: '编辑', icon: 'sliders', onClick: () => setEditing(a) },
                            { sep: true },
                            { label: '删除', icon: 'trash', danger: true, onClick: () => confirmDelete(a) },
                          ]}
                        />
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </BlockCard>
      </Blocks>

      {modalOpen && (
        <AnnouncementModal
          key={editing?.id ?? 'new'}
          initial={editing}
          onCancel={() => {
            setCreating(false);
            setEditing(null);
          }}
          onSubmit={v => (editing ? save(editing.id, v) : create(v))}
        />
      )}
    </>
  );
}
