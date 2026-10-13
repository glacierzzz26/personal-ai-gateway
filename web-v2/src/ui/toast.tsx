import type { ReactNode } from 'react';
import { create } from 'zustand';
import { Icon } from './icons';

type ToastType = 'ok' | 'err' | 'info' | 'warn';

interface ToastItem {
  id: number;
  msg: ReactNode;
  type: ToastType;
}

let seq = 1;

const useToastStore = create<{
  items: ToastItem[];
  push: (msg: ReactNode, type: ToastType) => void;
}>((set) => ({
  items: [],
  push: (msg, type) => {
    const id = seq++;
    set((s) => ({ items: [...s.items, { id, msg, type }] }));
    window.setTimeout(() => {
      set((s) => ({ items: s.items.filter((i) => i.id !== id) }));
    }, 2600);
  },
}));

/** 原型 toast(msg,type) 的 React 版：可在任意非组件代码里调用。 */
export function toast(msg: ReactNode, type: ToastType = 'info') {
  useToastStore.getState().push(msg, type);
}

const TOAST_ICON: Record<ToastType, 'okCircle' | 'xCircle' | 'info' | 'warn'> = {
  ok: 'okCircle',
  err: 'xCircle',
  info: 'info',
  warn: 'warn',
};

/** 挂载一次；渲染原型 `.toast-root`。 */
export function Toaster() {
  const items = useToastStore((s) => s.items);
  return (
    <div className="toast-root">
      {items.map((t) => (
        <div key={t.id} className={`toast ${t.type}`}>
          <Icon name={TOAST_ICON[t.type]} size={14} />
          <span>{t.msg}</span>
        </div>
      ))}
    </div>
  );
}

/**
 * antd `App.useApp().message` 的同名替身 —— 调用点只需换 import,
 * `message.success(...)` 之类保持原样。warning 走 warn 语义色。
 */
export const message = {
  success: (m: ReactNode) => toast(m, 'ok'),
  error: (m: ReactNode) => toast(m, 'err'),
  info: (m: ReactNode) => toast(m, 'info'),
  warning: (m: ReactNode) => toast(m, 'warn'),
};
