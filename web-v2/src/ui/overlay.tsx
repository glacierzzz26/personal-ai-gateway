import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { ReactNode } from 'react';
import { create } from 'zustand';
import clsx from 'clsx';
import { Icon } from './icons';
import type { IconName } from './icons';

/* ============================================================
   单一覆盖层（对齐原型 #layer）：Modal / Drawer 都挂在这里，
   支持浅栈（抽屉上再开确认框），禁止无限嵌套。
   ============================================================ */
interface Entry {
  id: number;
  node: ReactNode;
}

let seq = 1;
export function nextOverlayId() {
  return seq++;
}

const useOverlayStore = create<{ stack: Entry[] }>(() => ({ stack: [] }));

export function mountOverlay(id: number, node: ReactNode) {
  useOverlayStore.setState((s) => ({ stack: [...s.stack, { id, node }] }));
}

export function openOverlay(node: ReactNode): number {
  const id = nextOverlayId();
  mountOverlay(id, node);
  return id;
}

export function closeOverlay(id?: number) {
  useOverlayStore.setState((s) => {
    if (!s.stack.length) return s;
    const target = id ?? s.stack[s.stack.length - 1].id;
    return { stack: s.stack.filter((e) => e.id !== target) };
  });
}

export function closeAllOverlays() {
  useOverlayStore.setState({ stack: [] });
}

/** 挂载一次（App 根部）。Esc 关栈顶。 */
export function OverlayHost() {
  const stack = useOverlayStore((s) => s.stack);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      const s = useOverlayStore.getState().stack;
      if (s.length) closeOverlay(s[s.length - 1].id);
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);
  return (
    <>
      {stack.map((e, i) => (
        <div key={e.id} className="overlay" style={{ zIndex: 100 + i * 100 }}>
          {e.node}
        </div>
      ))}
    </>
  );
}

/* ------------------------------------------------------------ */

export function Modal({
  title,
  sub,
  width,
  footer,
  onClose,
  closable = true,
  children,
}: {
  title: ReactNode;
  sub?: ReactNode;
  width?: number;
  footer?: ReactNode;
  onClose: () => void;
  closable?: boolean;
  children: ReactNode;
}) {
  return (
    <div className="modal" style={width ? { maxWidth: width } : undefined}>
      <div className="modal-head">
        <div style={{ flex: 1, minWidth: 0 }}>
          <h3>{title}</h3>
          {sub && <div className="sub">{sub}</div>}
        </div>
        {closable && (
          <button className="icon-btn" onClick={onClose} aria-label="关闭">
            <Icon name="x" size={13} />
          </button>
        )}
      </div>
      <div className="modal-body">{children}</div>
      {footer && <div className="modal-foot">{footer}</div>}
    </div>
  );
}

export function Drawer({
  title,
  sub,
  width,
  extra,
  footer,
  onClose,
  children,
}: {
  title: ReactNode;
  sub?: ReactNode;
  width?: number;
  extra?: ReactNode;
  footer?: ReactNode;
  onClose: () => void;
  children: ReactNode;
}) {
  return (
    <div className="drawer" style={width ? { width } : undefined}>
      <div className="drawer-head">
        <div style={{ flex: 1, minWidth: 0 }}>
          <h3>{title}</h3>
          {sub && <div className="sub">{sub}</div>}
        </div>
        {extra}
        <button className="icon-btn" onClick={onClose} aria-label="关闭">
          <Icon name="x" size={13} />
        </button>
      </div>
      <div className="drawer-body">{children}</div>
      {footer && <div className="drawer-foot">{footer}</div>}
    </div>
  );
}

export function showModal(o: {
  title: ReactNode;
  sub?: ReactNode;
  width?: number;
  body: ReactNode;
  foot?: ReactNode;
  closable?: boolean;
}): number {
  const id = nextOverlayId();
  mountOverlay(
    id,
    <Modal title={o.title} sub={o.sub} width={o.width} footer={o.foot} closable={o.closable} onClose={() => closeOverlay(id)}>
      {o.body}
    </Modal>,
  );
  return id;
}

export function showDrawer(o: {
  title: ReactNode;
  sub?: ReactNode;
  width?: number;
  body: ReactNode;
}): number {
  const id = nextOverlayId();
  mountOverlay(
    id,
    <Drawer title={o.title} sub={o.sub} width={o.width} onClose={() => closeOverlay(id)}>
      {o.body}
    </Drawer>,
  );
  return id;
}

/**
 * 确认框 —— 替代 antd `modal.confirm`。
 * 所有现有用法都以「await 结果再决定是否提交 mutation」为形,故保留 Promise 语义。
 */
export function confirm(o: {
  title: ReactNode;
  content?: ReactNode;
  okText?: ReactNode;
  cancelText?: ReactNode;
  danger?: boolean;
}): Promise<boolean> {
  return new Promise((resolve) => {
    const decide = (v: boolean) => {
      closeOverlay(id);
      resolve(v);
    };
    const id = showModal({
      title: o.title,
      body: o.content ?? null,
      foot: (
        <>
          <button className="btn btn-secondary" onClick={() => decide(false)}>
            {o.cancelText ?? '取消'}
          </button>
          <button className={clsx('btn', o.danger ? 'btn-danger' : 'btn-primary')} onClick={() => decide(true)}>
            {o.okText ?? '确定'}
          </button>
        </>
      ),
    });
  });
}

/* ============================================================
   下拉菜单 / 气泡确认（页面流内绝对定位，配合父级 .rel）
   ============================================================ */
export interface MenuItem {
  sep?: true;
  label?: ReactNode;
  icon?: IconName;
  danger?: boolean;
  onClick?: () => void;
}

export function Menu({ items, onClose }: { items: MenuItem[]; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    // 延后一帧挂监听，避免「打开菜单的那次点击」立刻把它关掉。
    const onDoc = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    const t = window.setTimeout(() => document.addEventListener('mousedown', onDoc), 0);
    document.addEventListener('keydown', onKey);
    return () => {
      window.clearTimeout(t);
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, [onClose]);
  return (
    <div className="menu" ref={ref}>
      {items.map((it, i) =>
        it.sep ? (
          <div key={i} className="menu-sep" />
        ) : (
          <div
            key={i}
            className={clsx('menu-item', it.danger && 'danger')}
            onClick={() => {
              onClose();
              it.onClick?.();
            }}
          >
            {it.icon && <Icon name={it.icon} size={13} />}
            <span>{it.label}</span>
          </div>
        ),
      )}
    </div>
  );
}

/** 菜单开关 + 外侧点击关闭；父级需为 `.rel`。由调用方持有 open 状态。 */

/**
 * 表格行内的「更多」下拉。**走 portal + fixed 定位** —— 表格外层 `.table-wrap` 是
 * `overflow-x:auto` 的滚动容器,会裁掉/困住单元格里绝对定位的 `.menu`(菜单会被
 * 截断或把表格撑出滚动条)。挂到 body 上按触发按钮的位置 fixed 放置,即不受裁剪影响。
 * 滚动 / 缩放即关闭,免得菜单与已移动的行错位。
 */
export function Dropdown({
  items,
  icon = 'dots',
  title,
  ariaLabel = '更多操作',
}: {
  items: MenuItem[];
  icon?: IconName;
  title?: string;
  ariaLabel?: string;
}) {
  const btnRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; right: number }>({ top: 0, right: 0 });

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (btnRef.current && btnRef.current.contains(e.target as Node)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    const onMove = () => setOpen(false);
    const t = window.setTimeout(() => document.addEventListener('mousedown', onDoc), 0);
    document.addEventListener('keydown', onKey);
    window.addEventListener('scroll', onMove, true);
    window.addEventListener('resize', onMove);
    return () => {
      window.clearTimeout(t);
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
      window.removeEventListener('scroll', onMove, true);
      window.removeEventListener('resize', onMove);
    };
  }, [open]);

  const toggle = () => {
    if (!open && btnRef.current) {
      const r = btnRef.current.getBoundingClientRect();
      setPos({ top: r.bottom + 4, right: Math.max(6, window.innerWidth - r.right) });
    }
    setOpen((o) => !o);
  };

  return (
    <>
      <button ref={btnRef} type="button" className="btn btn-ghost btn-sm" aria-label={ariaLabel} title={title} onClick={toggle}>
        <Icon name={icon} size={13} />
      </button>
      {open &&
        createPortal(
          <div className="menu" style={{ position: 'fixed', top: pos.top, right: pos.right, zIndex: 150 }}>
            {items.map((it, i) =>
              it.sep ? (
                <div key={i} className="menu-sep" />
              ) : (
                <div
                  key={i}
                  className={clsx('menu-item', it.danger && 'danger')}
                  onClick={() => {
                    setOpen(false);
                    it.onClick?.();
                  }}
                >
                  {it.icon && <Icon name={it.icon} size={13} />}
                  <span>{it.label}</span>
                </div>
              ),
            )}
          </div>,
          document.body,
        )}
    </>
  );
}

export function Popconfirm({
  open,
  text,
  okText = '删除',
  cancelText = '取消',
  danger = true,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  text: ReactNode;
  okText?: ReactNode;
  cancelText?: ReactNode;
  danger?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  if (!open) return null;
  return (
    <div className="popconfirm">
      <p>{text}</p>
      <div className="pc-foot">
        <button className="btn btn-secondary btn-sm" onClick={onCancel}>
          {cancelText}
        </button>
        <button className={clsx('btn btn-sm', danger ? 'btn-danger' : 'btn-primary')} onClick={onConfirm}>
          {okText}
        </button>
      </div>
    </div>
  );
}
