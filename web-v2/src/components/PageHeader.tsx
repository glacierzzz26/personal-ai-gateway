import type { ReactNode } from 'react';

interface Props {
  title: string;
  desc?: ReactNode;
  /** 本页的操作 —— 只有页面头放本页操作，区块内部不再放导航级按钮 */
  extra?: ReactNode;
}

export default function PageHeader({ title, desc, extra }: Props) {
  return (
    <div className="page-head">
      <div style={{ minWidth: 0 }}>
        <h1>{title}</h1>
        {desc && <div className="sub">{desc}</div>}
      </div>
      {extra && <div className="head-actions">{extra}</div>}
    </div>
  );
}
