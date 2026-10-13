interface Props {
  name: string;
  size?: number;
  /** name 为空(非单一厂商的聚合渠道)时的替代字形,如 'CC' / 'OC' / '三方' */
  fallback?: string;
}

/** 供应商标识：首字母单色方块，禁止彩色 logo */
export default function ProviderMark({ name, size = 20, fallback }: Props) {
  const mark = name || fallback || '—';
  return (
    <span
      style={{
        width: size, height: size, flex: `0 0 ${size}px`, borderRadius: 'var(--gw-r-badge)',
        background: 'var(--gw-bg)', color: 'var(--gw-text-2)',
        fontSize: mark.length > 1 ? 9 : size <= 20 ? 11 : 13, fontWeight: 600,
        display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
      }}
    >
      {mark.slice(0, 2)}
    </span>
  );
}
