import { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { NAV_ITEMS } from '@/layout/nav';
import { useUi } from '@/stores/ui';

/**
 * Ctrl+K 快速跳转面板。
 * 只跳页面，不做资源检索 —— 保持「命令面板」语义单一。
 * 键盘：↑↓ 选择 · Enter 进入 · Esc 关闭（全站可达）。
 */
export default function CmdK() {
  const open = useUi(s => s.cmdkOpen);
  const setOpen = useUi(s => s.setCmdkOpen);
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [q, setQ] = useState('');
  const [active, setActive] = useState(0);

  const items = useMemo(() => {
    const s = q.trim().toLowerCase();
    if (!s) return NAV_ITEMS;
    return NAV_ITEMS.filter(it => it.label.toLowerCase().includes(s) || it.group.toLowerCase().includes(s));
  }, [q]);

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
      // 等面板挂载后再聚焦
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  // 结果变化时把选中项拉回可视区
  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>('[data-active="1"]');
    el?.scrollIntoView({ block: 'nearest' });
  }, [active, items]);

  if (!open) return null;

  return (
    <div
      className="gw-cmdk-mask"
      onMouseDown={e => {
        if (e.target === e.currentTarget) setOpen(false);
      }}
    >
      <div className="gw-palette" role="dialog" aria-modal="true" aria-label="快速跳转">
        <input
          ref={inputRef}
          value={q}
          onChange={e => {
            setQ(e.target.value);
            setActive(0);
          }}
          placeholder="跳转到页面，或输入关键词…"
          aria-label="搜索页面"
        />
        <div className="gw-p-list" ref={listRef} role="listbox">
          {items.length === 0 ? (
            <div className="gw-p-empty">没有匹配「{q}」的页面</div>
          ) : (
            items.map((it, i) => (
              <button
                key={it.key}
                type="button"
                className="gw-p-item"
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
                <span className="gw-p-g">{it.group}</span>
              </button>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
