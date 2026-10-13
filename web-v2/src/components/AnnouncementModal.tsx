import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/services/api';
import type { AnnouncementLevel } from '@/types';
import { Modal } from '@/ui/overlay';
import { Btn } from '@/ui/primitives';

/** 公告级别 → 语义色/文字。颜色只是辅助，必须同时给出文字(无障碍红线)。 */
const LEVEL_META: Record<AnnouncementLevel, { tone: 'ok' | 'warn' | 'err' | 'aux'; label: string }> = {
  info: { tone: 'aux', label: '通知' },
  warn: { tone: 'warn', label: '注意' },
  danger: { tone: 'err', label: '重要' },
};

/** tone → CSS 变量色（替代原 antd 时代的 TONE_COLOR，统一定义在此避免再引 @/utils/format）。 */
const TONE_VAR: Record<string, string> = { ok: 'var(--ok)', warn: 'var(--warn)', err: 'var(--err)', aux: 'var(--brand)' };
/** tone → 设计系统状态点字母（.dot 的 g/a/r/s，对应 ok/warn/err/slate）。 */
const TONE_DOT: Record<string, string> = { ok: 'g', warn: 'a', err: 'r', aux: 's' };

/**
 * 全站公告弹窗:登录后自动拉取「我最新一条未确认公告」，有则弹出。
 * 挂在 AppLayout，覆盖管理员与普通用户、覆盖所有页面。
 *
 * 只能点「我已知晓」关闭(maskClosable 关、ESC/x 均为 no-op)—— 这正是需求:
 * 点了我已知晓才不再显示。确认后缓存失效重拉,若还有次新的未读公告则继续弹出。
 * 正文自然换行、不设 maxHeight,故弹窗内不出现滚动框。
 */
export default function AnnouncementModal() {
  const qc = useQueryClient();
  const { data } = useQuery({
    queryKey: ['me-announcement'],
    queryFn: api.myAnnouncement,
    staleTime: 0,
    refetchOnWindowFocus: false,
    retry: 0,
  });
  const current = data?.announcement ?? null;

  const ack = useMutation({
    mutationFn: (id: number) => api.ackAnnouncement(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['me-announcement'] }),
  });

  if (!current) return null;
  const meta = LEVEL_META[current.level] ?? LEVEL_META.info;
  const color = TONE_VAR[meta.tone];

  return (
    <Modal
      open
      width={480}
      maskClosable={false}
      onClose={() => {}}
      footer={
        <Btn variant="primary" loading={ack.isPending} onClick={() => current && ack.mutate(current.id)}>
          我已知晓
        </Btn>
      }
    >
      <div style={{ paddingTop: 4 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 14 }}>
          <span className={`dot ${TONE_DOT[meta.tone]}`} />
          <span style={{ fontSize: 13, fontWeight: 500, color }}>{meta.label}</span>
        </div>
        <div style={{ borderLeft: `3px solid ${color}`, paddingLeft: 14, marginBottom: 4 }}>
          <h3 style={{ margin: '0 0 10px', fontSize: 17, fontWeight: 600, color: 'var(--t1)' }}>
            {current.title}
          </h3>
          <div
            style={{
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
              fontSize: 14,
              lineHeight: 1.75,
              color: 'var(--t2)',
            }}
          >
            {current.body}
          </div>
        </div>
      </div>
    </Modal>
  );
}
