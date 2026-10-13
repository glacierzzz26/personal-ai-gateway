import type { ReactNode } from 'react';
import { PageHead } from '@/ui/primitives';

interface Props {
  title: ReactNode;
  desc?: ReactNode;
  /** 本页的操作 —— 只有页面头放本页操作，区块内部不再放导航级按钮 */
  extra?: ReactNode;
}

export default function PageHeader({ title, desc, extra }: Props) {
  return <PageHead title={title} desc={desc} actions={extra} />;
}
