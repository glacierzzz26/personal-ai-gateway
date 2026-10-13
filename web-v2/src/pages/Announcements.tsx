import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { Block as BlockCard, Blocks } from '@/components/Block';
import { EmptyState, ErrorState } from '@/components/States';
import { Btn, Field, Input, PageHead, Select, Switch } from '@/ui/primitives';
import { confirm, Dropdown, Modal } from '@/ui/overlay';
import { toast } from '@/ui/toast';
import { Icon } from '@/ui/icons';
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

/** 语义 tone → .dot 色点类（prototype.css：g=正常 a=警示 r=错误 s=灰） */
const DOT: Record<Tone, 'g' | 'a' | 'r' | 's'> = { ok: 'g', warn: 'a', err: 'r', aux: 's' };

/** 级别徽章:色点 + 文字(不靠颜色单独表意)。 */
function LevelBadge({ level }: { level: AnnouncementLevel }) {
  const m = LEVEL_META[level] ?? LEVEL_META.info;
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, color: TONE_COLOR[m.tone] }}>
      <i className={`dot ${DOT[m.tone]}`} />
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

/** datetime-local 输入值 ↔ RFC3339。 */
const toLocalInput = (v?: string | null) => (v ? dayjs(v).format('YYYY-MM-DDTHH:mm') : '');
const fromLocalInput = (v: string) => (v ? dayjs(v).toISOString() : null);

/** 新建/编辑公告弹窗。 */
function AnnouncementModal(props: {
  initial: AnnouncementItem | null;
  open: boolean;
  onCancel: () => void;
  onSubmit: (v: AnnouncementDraft) => Promise<void>;
}) {
  const { initial, open, onCancel, onSubmit } = props;
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [level, setLevel] = useState<AnnouncementLevel>('info');
  const [enabled, setEnabled] = useState(true);
  const [publishAt, setPublishAt] = useState('');
  const [expiresAt, setExpiresAt] = useState('');
  const [errs, setErrs] = useState<{ title?: string; body?: string }>({});
  const [saving, setSaving] = useState(false);

  // 新建/编辑共用:initial 非空即编辑。默认值随 initial 水合(key 变化会重挂载)。
  useEffect(() => {
    if (!open) return;
    setTitle(initial?.title ?? '');
    setBody(initial?.body ?? '');
    setLevel(initial?.level ?? 'info');
    setEnabled(initial?.enabled ?? true);
    setPublishAt(toLocalInput(initial?.publishAt));
    setExpiresAt(toLocalInput(initial?.expiresAt));
    setErrs({});
  }, [open, initial]);

  const submit = async () => {
    // 校验规则照搬原表单 rules(required + whitespace)
    const e: typeof errs = {};
    if (!title.trim()) e.title = '请输入标题';
    if (!body.trim()) e.body = '请输入正文';
    setErrs(e);
    if (Object.keys(e).length > 0) return;
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
      toast.err(`保存失败:${errMsg(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={initial ? '编辑公告' : '发布公告'}
      open={open}
      onClose={onCancel}
      width={560}
      footer={
        <>
          <Btn onClick={onCancel}>取消</Btn>
          <Btn variant="primary" loading={saving} onClick={submit}>{initial ? '保存' : '发布'}</Btn>
        </>
      }
    >
      <Field label="标题" err={errs.title}>
        <Input placeholder="例如:系统维护通知" maxLength={120} value={title} err={!!errs.title}
          onChange={e => setTitle(e.target.value)} />
      </Field>
      <Field label="正文" err={errs.body} hint={`${body.length}/2000`}>
        <textarea
          className="input"
          rows={5}
          placeholder="公告内容，支持换行"
          maxLength={2000}
          value={body}
          style={{ resize: 'vertical' }}
          onChange={e => setBody(e.target.value)}
        />
      </Field>
      <div className="form-grid">
        <Field label="级别">
          <Select value={level} onChange={e => setLevel(e.target.value as AnnouncementLevel)}>
            <option value="info">通知</option>
            <option value="warn">注意</option>
            <option value="danger">重要</option>
          </Select>
        </Field>
        <Field label="启用" hint="停用后立即不再对任何用户弹出">
          <Switch checked={enabled} onChange={setEnabled} label={enabled ? '已启用' : '已停用'} />
        </Field>
        <Field label="定时发布" hint="留空 = 立即发布">
          <Input type="datetime-local" value={publishAt} onChange={e => setPublishAt(e.target.value)} />
        </Field>
        <Field label="过期时间" hint="留空 = 永不过期">
          <Input type="datetime-local" value={expiresAt} onChange={e => setExpiresAt(e.target.value)} />
        </Field>
      </div>
    </Modal>
  );
}

export default function Announcements() {
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
    toast.ok('公告已发布');
    setCreating(false);
    refresh();
  };

  const save = async (id: number, v: AnnouncementDraft) => {
    await api.updateAnnouncement(id, v);
    toast.ok('公告已保存');
    setEditing(null);
    refresh();
  };

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteAnnouncement(id),
    onSuccess: () => { toast.ok('已删除'); refresh(); },
    onError: e => toast.err(`删除失败:${errMsg(e)}`),
  });

  const confirmDelete = (a: AnnouncementItem) => {
    confirm({
      title: `删除公告「${a.title}」?`,
      body: '删除后该公告不再对任何用户弹出，相关「已读」记录一并清除。',
      okText: '删除',
      danger: true,
      cancelText: '取消',
    }).then(ok => { if (ok) void remove.mutateAsync(a.id); });
  };

  return (
    <div>
      <PageHead
        title="公告管理"
        sub="发布后所有登录用户会在弹窗中看到；点「我已知晓」后该公告不再对其显示"
        actions={<Btn variant="primary" icon="megaphone" onClick={() => setCreating(true)}>发布公告</Btn>}
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
            <div className="table-wrap" style={{ padding: '8px 6px 0' }}>
              <table className="tb" style={{ tableLayout: 'fixed' }}>
                <thead>
                  <tr>
                    <th>标题</th>
                    <th style={{ width: 90 }}>级别</th>
                    <th style={{ width: 96 }}>状态</th>
                    <th style={{ width: 132 }}>定时发布</th>
                    <th style={{ width: 132 }}>过期</th>
                    <th className="right" style={{ width: 96 }}>已读</th>
                    <th className="right" style={{ width: 72 }}>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {isLoading && data.length === 0 ? (
                    <tr><td colSpan={7} style={{ textAlign: 'center', color: 'var(--t3)', padding: 24 }}>读取中…</td></tr>
                  ) : data.length === 0 ? (
                    <tr>
                      <td colSpan={7}>
                        <EmptyState
                          title="还没有公告"
                          desc="发布一条公告，所有登录用户在进入后台时会看到弹窗。"
                          action={<Btn size="sm" variant="primary" onClick={() => setCreating(true)}>发布公告</Btn>}
                        />
                      </td>
                    </tr>
                  ) : (
                    data.map(r => {
                      const s = statusOf(r);
                      return (
                        <tr key={r.id}>
                          {/* 唯一的弹性列:吸收剩余宽度,标题 attr 替代 Tooltip */}
                          <td style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={`${r.title}\n${r.body}`}>
                            <b style={{ fontWeight: 500, color: 'var(--t1)' }}>{r.title}</b>
                            <span style={{ color: 'var(--t3)', marginLeft: 8 }}>{r.body.slice(0, 24)}</span>
                          </td>
                          <td><LevelBadge level={r.level} /></td>
                          <td>
                            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                              <i className={`dot ${DOT[s.tone]}`} />
                              {s.label}
                            </span>
                          </td>
                          <td>
                            {r.publishAt
                              ? <span className="num" style={{ color: 'var(--t3)' }}>{dayjs(r.publishAt).format('MM-DD HH:mm')}</span>
                              : <span style={{ color: 'var(--t3)' }}>立即</span>}
                          </td>
                          <td>
                            {r.expiresAt
                              ? <span className="num" style={{ color: 'var(--t3)' }}>{dayjs(r.expiresAt).format('MM-DD HH:mm')}</span>
                              : <span style={{ color: 'var(--t3)' }}>永不</span>}
                          </td>
                          <td className="right">
                            <span className="num" title={`${r.readCount ?? 0} / ${r.userTotal ?? 0} 名普通用户已确认`}>
                              {fmt.n(r.readCount ?? 0)}/{fmt.n(r.userTotal ?? 0)}
                            </span>
                          </td>
                          <td className="right">
                            <Dropdown
                              menu={[
                                { key: 'edit', label: '编辑', icon: 'file', onClick: () => setEditing(r) },
                                { key: 'delete', label: '删除', icon: 'trash', danger: true, onClick: () => confirmDelete(r) },
                              ]}
                            >
                              {() => (
                                <button type="button" className="icon-btn" aria-label={`更多操作 ${r.title}`}>
                                  <Icon n="more" />
                                </button>
                              )}
                            </Dropdown>
                          </td>
                        </tr>
                      );
                    })
                  )}
                </tbody>
              </table>
            </div>
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
