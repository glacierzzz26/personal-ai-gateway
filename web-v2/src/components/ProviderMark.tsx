interface Props {
  name: string;
  size?: number;
}

/** 供应商标识：首字母单色方块，禁止彩色 logo */
export default function ProviderMark({ name, size = 20 }: Props) {
  return (
    <span
      style={{
        width: size, height: size, flex: `0 0 ${size}px`, borderRadius: 'var(--gw-r-badge)',
        background: 'var(--gw-bg)', color: 'var(--gw-text-2)',
        fontSize: size <= 20 ? 11 : 13, fontWeight: 600,
        display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
      }}
    >
      {name.slice(0, 1)}
    </span>
  );
}
