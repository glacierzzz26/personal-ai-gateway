import type { Tone } from '@/utils/format';
import { STATUS, TONE_COLOR } from '@/utils/format';

interface Props {
  status: string;
  /** 覆盖文案（例如日志里既要显示成功/失败又要带状态码） */
  text?: string;
  /** 覆盖语义色（用于非枚举状态，如"中断"） */
  tone?: Tone;
  children?: React.ReactNode;
}

/**
 * 状态一律用「圆点 + 文字」，禁止彩色胶囊块。
 * 颜色只是辅助 —— 文字必须能独立表达状态（色盲/高对比场景）。
 */
export default function StatusDot({ status, text, tone, children }: Props) {
  const s = STATUS[status];
  const t = tone ?? s?.tone ?? 'aux';
  return (
    <span className="gw-st" style={{ fontSize: 13 }}>
      <i className={`gw-dot ${t}`} style={{ background: TONE_COLOR[t] }} />
      {text ?? s?.t ?? status}
      {children}
    </span>
  );
}
