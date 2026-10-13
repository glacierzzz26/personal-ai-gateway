import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useNavigate } from 'react-router-dom';
import { visibleNav } from '@/layout/nav';
import { Icon } from '@/ui/icons';
import { useSession } from '@/stores/session';
import { useUi } from '@/stores/ui';

/**
 * Ctrl+K 快速跳转面板（原型 cmd 同款：分组列表 + 键盘导航）。
 * 只跳页面，不做资源检索 —— 保持「命令面板」语义单一。
 * 键盘：↑↓ 选择 · Enter 进入 · Esc 关闭（全站可达）。
 */
export default function CmdK() {
  const open = useUi(s => s.cmdkOpen);
  const setOpen = useUi(s => s.setCmdkOpen);
  const navigate = useNavigate();
  const isAdmin = useSession(s => s.admin?.role === 'admin');
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [q, setQ] = useState('');
  const [active, setActive] = useState(0);

  // 与侧栏同一份可见性规则:普通用户看不到管理面页面,面板里也不该出现。
  const items = useMemo(() => {
    const nav = visibleNav(isAdmin);
    const s = q.trim().toLowerCase();
    if (!s) return nav;
    return nav.filter(it => it.label.toLowerCase().includes(s) || it.group.toLowerCase().includes(s));
  }, [q, isAdmin]);

  // 全局快捷键（含打开态下的方向键）
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setOpen(!useUi.getState().cmdkOpen);
        return;
      }
      if (!useUi.getState().cmdkOpen) return;
      if (e.key === 'Escape') {
        e.preventDefault();
        setOpen(false);
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        setActive(a => Math.min(items.length - 1, a + 1));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setActive(a => Math.max(0, a - 1));
      } else if (e.key === 'Enter') {
        const it = items[active];
        if (it) {
          e.preventDefault();
          setOpen(false);
          navigate(it.key);
        }
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [items, active, navigate, setOpen]);

  // 打开时重置并聚焦
  useEffect(() => {
    if (open) {
      setQ('');
      setActive(0);
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  // 结果变化时把选中项拉回可视区
  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>('[data-active="1"]');
    el?.scrollIntoView({ block: 'nearest' });
  }, [active, items]);

  if (!open || typeof document === 'undefined') return null;

  return createPortal(
    <div
      className="overlay"
      style={{ alignItems: 'flex-start', paddingTop: '12vh' }}
      onMouseDown={e => {
        if (e.target === e.currentTarget) setOpen(false);
      }}
    >
      <div className="modal" role="dialog" aria-modal="true" aria-label="快速跳转" style={{ maxWidth: 480 }}>
        <div style={{ padding: '10px 12px', borderBottom: '1px solid var(--line)', display: 'flex', alignItems: 'center', gap: 8 }}>
          <Icon n="search" s={15} style={{ color: 'var(--t3)', flex: '0 0 15px' }} />
          <input
            ref={inputRef}
            value={q}
            onChange={e => {
              setQ(e.target.value);
              setActive(0);
            }}
            placeholder="跳转到页面，或输入关键词…"
            aria-label="搜索页面"
            className="input"
            style={{ background: 'transparent', border: 'none', height: 30, padding: 0 }}
          />
          <span className="kbd">Esc</span>
        </div>
        <div className="cmd-list" ref={listRef} role="listbox">
          {items.length === 0 ? (
            <div className="cmd-item" style={{ cursor: 'default', color: 'var(--t3)' }}>没有匹配「{q}」的页面</div>
          ) : (
            items.map((it, i) => (
              <div
                key={it.key}
                className={`cmd-item${i === active ? ' sel' : ''}`}
                role="option"
                aria-selected={i === active}
                data-active={i === active ? '1' : '0'}
                onMouseMove={() => setActive(i)}
                onClick={() => {
                  setOpen(false);
                  navigate(it.key);
                }}
              >
                {it.icon}
                <span>{it.label}</span>
                <span className="k">{it.group}</span>
              </div>
            ))
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
