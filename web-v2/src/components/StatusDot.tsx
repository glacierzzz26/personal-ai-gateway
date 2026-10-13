type Tone = 'ok' | 'warn' | 'err' | 'aux';

interface Props {
  /** 状态枚举名（省略时必须给 text/tone，如日志的成功/失败列） */
  status?: string;
  /** 覆盖文案（例如日志里既要显示成功/失败又要带状态码） */
  text?: string;
  /** 覆盖语义色（用于非枚举状态，如"中断"） */
  tone?: Tone;
  children?: React.ReactNode;
}

/** 状态 → 语义色（与后端状态码同源，精简自 format.STATUS，避免依赖已下线的旧模块） */
const STATUS: Record<string, { t: string; tone: Tone }> = {
  healthy: { t: '健康', tone: 'ok' },
  degraded: { t: '降级', tone: 'warn' },
  down: { t: '不可用', tone: 'err' },
  disabled: { t: '已停用', tone: 'aux' },
  unknown: { t: '待观察', tone: 'aux' },
  active: { t: '正常', tone: 'ok' },
  expired: { t: '已过期', tone: 'err' },
};

/** 语义色 → 圆点类名（.dot 的 g/a/r/s） */
const DOT: Record<Tone, string> = { ok: 'g', warn: 'a', err: 'r', aux: 's' };

/**
 * 状态一律用「圆点 + 文字」，禁止彩色胶囊块。
 * 颜色只是辅助 —— 文字必须能独立表达状态（色盲/高对比场景）。
 */
export default function StatusDot({ status, text, tone, children }: Props) {
  const t: Tone = tone ?? STATUS[status ?? '']?.tone ?? 'aux';
  const label = text ?? STATUS[status ?? '']?.t ?? status;
  return (
    <span style={{ fontSize: 13, display: 'inline-flex', alignItems: 'center', gap: 6, color: 'var(--t2)' }}>
      <span className={`dot ${DOT[t]}`} />
      {label}
      {children}
    </span>
  );
}
