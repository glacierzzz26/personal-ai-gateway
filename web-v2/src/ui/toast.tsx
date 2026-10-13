/* ============================================================
   Toast —— antd message 的自绘同形替身。
   用法：toast.ok('已保存') / toast.err('失败') / toast.info('提示')
   React 树外可调用（services 层错误处理等），根组件挂 <ToastRoot />。
   ============================================================ */
import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { Icon } from './icons';

export type ToastKind = 'ok' | 'err' | 'info';
interface ToastItem { id: number; kind: ToastKind; text: string }

let items: ToastItem[] = [];
let seq = 0;
const listeners = new Set<() => void>();

function emit() { listeners.forEach(l => l()); }

function push(kind: ToastKind, text: string, ms = 2600) {
  const id = ++seq;
  items = [...items, { id, kind, text }];
  emit();
  window.setTimeout(() => {
    items = items.filter(t => t.id !== id);
    emit();
  }, ms);
}

export const toast = {
  ok: (t: string) => push('ok', t),
  err: (t: string) => push('err', t, 4200),
  info: (t: string) => push('info', t),
};

const ICONS: Record<ToastKind, string> = { ok: 'okCircle', err: 'xCircle', info: 'info' };

export function ToastRoot() {
  const [, force] = useState(0);
  useEffect(() => {
    const l = () => force(x => x + 1);
    listeners.add(l);
    return () => { listeners.delete(l); };
  }, []);
  if (typeof document === 'undefined') return null;
  return createPortal(
    <div className="toast-root">
      {items.map(t => (
        <div key={t.id} className={`toast ${t.kind}`}>
          <Icon n={ICONS[t.kind]} />
          <span>{t.text}</span>
        </div>
      ))}
    </div>,
    document.body,
  );
}
