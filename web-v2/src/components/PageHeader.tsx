import type { ReactNode } from 'react';

interface Props {
  title: string;
  desc?: ReactNode;
  /** 本页的操作 —— 只有页面头放本页操作，区块内部不再放导航级按钮 */
  extra?: ReactNode;
}

export default function PageHeader({ title, desc, extra }: Props) {
  return (
    <div className="gw-page-h">
      <div style={{ minWidth: 0 }}>
        <h1>{title}</h1>
        {desc && <p>{desc}</p>}
      </div>
      {extra && <div className="gw-acts">{extra}</div>}
    </div>
  );
}
