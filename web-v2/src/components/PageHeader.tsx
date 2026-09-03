import type { ReactNode } from 'react';

interface Props {
  title: string;
  desc?: string;
  extra?: ReactNode;
}

export default function PageHeader({ title, desc, extra }: Props) {
  return (
    <div
      style={{
        display: 'flex', alignItems: 'flex-start', gap: 16, marginBottom: 20,
      }}
    >
      <div style={{ minWidth: 0 }}>
        <div style={{ fontSize: 18, fontWeight: 600, letterSpacing: '-.2px' }}>{title}</div>
        {desc && <div style={{ fontSize: 13, color: 'var(--gw-text-3)', marginTop: 4 }}>{desc}</div>}
      </div>
      {extra && <div style={{ marginLeft: 'auto', display: 'flex', gap: 8, flexShrink: 0 }}>{extra}</div>}
    </div>
  );
}
