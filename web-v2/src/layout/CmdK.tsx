import { Fragment, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import clsx from 'clsx';
import { Icon } from '@/ui/icons';
import type { IconName } from '@/ui/icons';
import { visibleNav } from '@/layout/nav';
import { useSession } from '@/stores/session';
import { useUi } from '@/stores/ui';
import { useTheme } from '@/ui/theme';

/**
 * Ctrl+K 快速跳转面板（对齐原型 .cmd-list，仅页面跳转 + 少量操作）。
 * 键盘：↑↓ 选择 · Enter 执行 · Esc 关闭（全站可达）。
 */
interface CmdItem {
  group: string;
  icon: IconName;
  label: string;
  nav?: string;
  run?: () => void;
}

export default function CmdK() {
  const open = useUi(s => s.cmdkOpen);
  const setOpen = useUi(s => s.setCmdkOpen);
  const navigate = useNavigate();
  const isAdmin = useSession(s => s.admin?.role === 'admin');
  const toggleTheme = useTheme(s => s.toggle);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [q, setQ] = useState('');
  const [active, setActive] = useState(0);

  // 与侧栏同一份可见性规则:普通用户看不到管理面页面,面板里也不该出现。
  const all = useMemo<CmdItem[]>(() => {
    const navItems: CmdItem[] = visibleNav(isAdmin).map(it => ({
      group: it.group,
      icon: it.icon,
      label: it.label,
      nav: it.key,
    }));
    const ops: CmdItem[] = [
      { group: '操作', icon: 'moon', label: '切换深色 / 浅色模式', run: () => toggleTheme() },
      { group: '操作', icon: 'refresh', label: '刷新当前页数据', run: () => navigate(0) },
    ];
    return [...navItems, ...ops];
  }, [isAdmin, toggleTheme, navigate]);

  const items = useMemo(() => {
    const s = q.trim().toLowerCase();
    if (!s) return all;
    return all.filter(it => it.label.toLowerCase().includes(s) || it.group.toLowerCase().includes(s));
  }, [q, all]);

  const exec = (it: CmdItem) => {
    setOpen(false);
    if (it.nav) navigate(it.nav);
    else it.run?.();
  };

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
          exec(it);
        }
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, active, setOpen]);

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

  if (!open) return null;

  return (
    <div
      className="overlay"
      style={{ zIndex: 200 }}
      onMouseDown={e => {
        if (e.target === e.currentTarget) setOpen(false);
      }}
    >
      <div className="modal" style={{ maxWidth: 640 }} role="dialog" aria-modal="true" aria-label="搜索">
        <div className="modal-head">
          <div style={{ flex: 1, minWidth: 0 }}>
            <h3>搜索</h3>
            <div className="sub">页面 / 操作 · ↑↓ 选择 · Enter 执行</div>
          </div>
          <button className="icon-btn" onClick={() => setOpen(false)} aria-label="关闭">
            <Icon name="x" size={13} />
          </button>
        </div>
        <div className="modal-body">
          <input
            ref={inputRef}
            className="input"
            value={q}
            onChange={e => {
              setQ(e.target.value);
              setActive(0);
            }}
            placeholder="输入关键词过滤…"
            aria-label="搜索"
          />
          <div className="cmd-list" ref={listRef} role="listbox">
            {items.length === 0 ? (
              <div className="empty" style={{ padding: 18 }}>
                <p>无匹配结果</p>
              </div>
            ) : (
              items.map((it, i) => {
                const newGroup = i === 0 || items[i - 1].group !== it.group;
                return (
                  <Fragment key={`${it.group}-${it.label}`}>
                    {newGroup && <div className="cmd-group">{it.group}</div>}
                    <div
                      className={clsx('cmd-item', i === active && 'sel')}
                      role="option"
                      aria-selected={i === active}
                      data-active={i === active ? '1' : '0'}
                      onMouseMove={() => setActive(i)}
                      onClick={() => exec(it)}
                    >
                      <Icon name={it.icon} size={14} />
                      <span>{it.label}</span>
                      <span className="k">Enter</span>
                    </div>
                  </Fragment>
                );
              })
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
