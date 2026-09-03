import { STATUS_COLOR, STATUS_TEXT } from '@/utils/format';

interface Props {
  status: string;
  text?: string;
}

/** 状态一律用小圆点 + 文字，禁止彩色胶囊块 */
export default function StatusTag({ status, text }: Props) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', fontSize: 13 }}>
      <i
        style={{
          width: 6, height: 6, borderRadius: '50%', marginRight: 6,
          background: STATUS_COLOR[status] ?? '#94A3B8', display: 'inline-block',
        }}
      />
      {text ?? STATUS_TEXT[status] ?? status}
    </span>
  );
}
