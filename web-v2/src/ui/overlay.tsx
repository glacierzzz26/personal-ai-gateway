/* ============================================================
   覆盖层基建 —— Modal / Drawer / confirm / Dropdown（自绘）。
   要点（对齐原型并修掉上一版的层级坑）：
   - 所有层走同一「层栈」：挂载时分配 z-index（100 起，每层 +2），
     抽屉上可再开确认框、确认框上可再开下拉，永不互相压死。
   - Modal / Drawer 关闭带 120ms 动画后再卸载，避免闪断。
   - Dropdown / Popconfirm 用 portal + fixed 定位（绕开
     .table-wrap{overflow-x:auto} 对行内绝对定位菜单的裁剪）。
   ============================================================ */
import { useCallback, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Icon } from './icons';

/* ---------------- 层栈 ---------------- */
let layerSeq = 0;
const freeZ: number[] = [];
function allocZ(): number {
  const reuse = freeZ.pop();
  if (reuse !== undefined) return reuse;
  layerSeq += 1;
  return 100 + layerSeq * 2;
}
function releaseZ(z: number) { freeZ.push(z); }

/** 挂载即分配层级的容器：portal 到 body，z 受控 */
function Layer({ z, children }: { z: number; children: ReactNode }) {
  return createPortal(<div style={{ position: 'fixed', inset: 0, zIndex: z, pointerEvents: 'none' }}>{children}</div>, document.body);
}

/** ESC 关闭钩子 */
function useEsc(open: boolean, onClose: () => void) {
  useEffect(() => {
    if (!open) return;
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); onClose(); } };
    document.addEventListener('keydown', h, true);
    return () => document.removeEventListener('keydown', h, true);
  }, [open, onClose]);
}

/* ---------------- Modal ---------------- */
export function Modal({ open, onClose, title, sub, width = 640, footer, children, maskClosable = true }:
  { open: boolean; onClose: () => void; title?: ReactNode; sub?: ReactNode; width?: number; footer?: ReactNode; children: ReactNode; maskClosable?: boolean }) {
  const [z, setZ] = useState<number | null>(null);
  const [render, setRender] = useState(open);
  useEffect(() => {
    if (open) { setRender(true); setZ(allocZ()); }
    else { const t = window.setTimeout(() => { setRender(false); setZ(null); }, 120); return () => window.clearTimeout(t); }
  }, [open]);
  useEffect(() => () => { if (z !== null) releaseZ(z); }, [z]);
  useEsc(open, onClose);
  if (!render || z === null || typeof document === 'undefined') return null;
  return (
    <Layer z={z}>
      <div
        className="overlay"
        style={{ pointerEvents: 'auto', opacity: open ? 1 : 0, transition: 'opacity 100ms linear' }}
        onMouseDown={e => { if (maskClosable && e.target === e.currentTarget) onClose(); }}
      >
        <div className="modal" style={{ maxWidth: width, opacity: open ? 1 : 0, transform: open ? 'none' : 'scale(.99)' }}>
          <div className="modal-head">
            <div className="grow">
              <h3>{title}</h3>
              {sub ? <div className="sub">{sub}</div> : null}
            </div>
            <button className="icon-btn" onClick={onClose} title="关闭"><Icon n="x" /></button>
          </div>
          <div className="modal-body">{children}</div>
          {footer ? <div className="modal-foot">{footer}</div> : null}
        </div>
      </div>
    </Layer>
  );
}

/* ---------------- Drawer ---------------- */
export function Drawer({ open, onClose, title, sub, width = 540, children, footer }:
  { open: boolean; onClose: () => void; title: ReactNode; sub?: ReactNode; width?: number; children: ReactNode; footer?: ReactNode }) {
  const [z, setZ] = useState<number | null>(null);
  const [render, setRender] = useState(open);
  useEffect(() => {
    if (open) { setRender(true); setZ(allocZ()); }
    else { const t = window.setTimeout(() => { setRender(false); setZ(null); }, 120); return () => window.clearTimeout(t); }
  }, [open]);
  useEffect(() => () => { if (z !== null) releaseZ(z); }, [z]);
  useEsc(open, onClose);
  if (!render || z === null || typeof document === 'undefined') return null;
  return (
    <Layer z={z}>
      <div
        className="overlay"
        style={{ justifyContent: 'flex-end', padding: 0, pointerEvents: 'auto', opacity: open ? 1 : 0, transition: 'opacity 100ms linear' }}
        onMouseDown={e => { if (e.target === e.currentTarget) onClose(); }}
      >
        <div className="drawer" style={{ width, transform: open ? 'none' : 'translateX(16px)', opacity: open ? 1 : 0, transition: 'transform 120ms linear,opacity 120ms linear' }}>
          <div className="drawer-head">
            <div className="grow">
              <h3 style={{ fontSize: 15, fontWeight: 600, color: 'var(--t1)' }}>{title}</h3>
              {sub ? <div className="sub">{sub}</div> : null}
            </div>
            <button className="icon-btn" onClick={onClose} title="关闭"><Icon n="x" /></button>
          </div>
          <div className="drawer-body">{children}</div>
          {footer ? <div className="modal-foot">{footer}</div> : null}
        </div>
      </div>
    </Layer>
  );
}

/* ---------------- confirm（Promise 风格，可挂在任意层之上） ---------------- */
interface ConfirmOpts { title: ReactNode; body?: ReactNode; okText?: string; cancelText?: string; danger?: boolean }
interface ConfirmState extends ConfirmOpts { z: number; resolve: (v: boolean) => void }
let setConfirmFn: ((s: ConfirmState | null) => void) | null = null;

export function confirm(opts: ConfirmOpts): Promise<boolean> {
  return new Promise(resolve => {
    if (setConfirmFn) setConfirmFn({ ...opts, z: allocZ(), resolve });
    else resolve(false);
  });
}

/** 挂在 App 根部一次；承载 confirm() 队列 */
export function ConfirmHost() {
  const [st, setSt] = useState<ConfirmState | null>(null);
  useEffect(() => { setConfirmFn = setSt; return () => { setConfirmFn = null; }; }, []);
  const done = useCallback((v: boolean) => {
    if (!st) return;
    releaseZ(st.z);
    st.resolve(v);
    setSt(null);
  }, [st]);
  useEffect(() => {
    if (!st) return;
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape') done(false); };
    document.addEventListener('keydown', h, true);
    return () => document.removeEventListener('keydown', h, true);
  }, [st, done]);
  if (!st || typeof document === 'undefined') return null;
  return createPortal(
    <div className="overlay" style={{ zIndex: st.z, animation: 'fadeIn 100ms linear' }} onMouseDown={e => { if (e.target === e.currentTarget) done(false); }}>
      <div className="modal" style={{ maxWidth: 420 }}>
        <div className="modal-head"><h3>{st.title}</h3></div>
        <div className="modal-body">{st.body ?? null}</div>
        <div className="modal-foot">
          <button className="btn btn-secondary" onClick={() => done(false)}>{st.cancelText ?? '取消'}</button>
          <button className={`btn ${st.danger ? 'btn-danger' : 'btn-primary'}`} onClick={() => done(true)}>{st.okText ?? '确认'}</button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

/* ---------------- Dropdown（行内「更多」/ 头像菜单） ---------------- */
export interface MenuItem { key: string; label: ReactNode; icon?: string; danger?: boolean; disabled?: boolean; onClick?: () => void }
export function Dropdown({ menu, children, minWidth = 180 }:
  { menu: MenuItem[]; children: (open: boolean) => ReactNode; minWidth?: number }) {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null);
  const ref = useRef<HTMLDivElement | null>(null);
  const zRef = useRef<number | null>(null);
  const [z, setZ] = useState<number | null>(null);

  const open = pos !== null;
  const openAt = (el: HTMLElement) => {
    const r = el.getBoundingClientRect();
    let y = r.bottom + 6;
    const estH = menu.length * 30 + 14;
    if (y + estH > window.innerHeight - 8) y = Math.max(8, r.top - estH - 6);
    setPos({ x: r.right, y });
    zRef.current = allocZ(); setZ(zRef.current);
  };

  useEffect(() => {
    if (!open) return;
    const close = () => { if (zRef.current !== null) releaseZ(zRef.current); zRef.current = null; setPos(null); setZ(null); };
    const onDown = (e: MouseEvent) => {
      const t = e.target as Element;
      if (ref.current?.contains(t)) return;
      if (t.closest && t.closest('.menu')) return;
      close();
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    const onScroll = () => close();
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey, true);
    window.addEventListener('scroll', onScroll, true);
    window.addEventListener('resize', onScroll);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey, true);
      window.removeEventListener('scroll', onScroll, true);
      window.removeEventListener('resize', onScroll);
    };
  }, [open]);

  return (
    <span
      ref={ref}
      onClick={e => {
        if (open) { if (zRef.current !== null) releaseZ(zRef.current); setPos(null); setZ(null); return; }
        openAt(e.currentTarget as HTMLElement);
      }}
    >
      {children(open)}
      {open && z !== null && typeof document !== 'undefined'
        ? createPortal(
            <div className="menu" style={{ right: 'auto', top: pos!.y, left: Math.max(8, pos!.x - minWidth), minWidth, zIndex: z }}>
              {menu.map(m => (
                <div key={m.key} className={`menu-item${m.danger ? ' danger' : ''}`} style={m.disabled ? { opacity: .45, pointerEvents: 'none' } : undefined} onClick={() => { if (m.disabled) return; if (zRef.current !== null) releaseZ(zRef.current); zRef.current = null; setPos(null); setZ(null); m.onClick?.(); }}>
                  {m.icon ? <Icon n={m.icon} /> : null}
                  <span>{m.label}</span>
                </div>
              ))}
            </div>,
            document.body,
          )
        : null}
    </span>
  );
}

/** 依赖注入上下文（保留扩展点）；当前实现只挂 ConfirmHost。 */
export function OverlayHost() { return <><ConfirmHost /></>; }
