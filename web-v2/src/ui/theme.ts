/* ============================================================
   主题 —— html[data-theme] 驱动的暗/亮双主题。
   默认规则（与原型一致）：
   - 登录前 / 会话恢复前 → 暗色（核心场景）
   - 会话恢复后，用户未手动切过（localStorage 无 gw-theme）→
     admin→暗色 / user→亮色
   - 手动切换持久化到 localStorage['gw-theme']，此后不再跟随角色
   ============================================================ */
import { create } from 'zustand';

const KEY = 'gw-theme';

export type ThemeMode = 'dark' | 'light';

interface ThemeState {
  mode: ThemeMode;
  /** 用户是否手动切过（切过后不再跟随角色默认） */
  manual: boolean;
  setMode: (m: ThemeMode, manual?: boolean) => void;
  toggle: () => void;
  /** 会话身份变化时调用：未手动切过则跟随角色默认 */
  syncRole: (role: 'admin' | 'user' | null) => void;
}

function applyTheme(m: ThemeMode) {
  const el = document.documentElement;
  // 切换期间加过渡动画类，140ms 后移除（避免首帧/高频切换时全局 transition 常驻）
  el.classList.add('theme-anim');
  el.setAttribute('data-theme', m);
  window.setTimeout(() => el.classList.remove('theme-anim'), 160);
}

const stored = typeof localStorage !== 'undefined' ? (localStorage.getItem(KEY) as ThemeMode | null) : null;
const initial: ThemeMode = stored === 'light' ? 'light' : 'dark';

if (typeof document !== 'undefined' && !document.documentElement.getAttribute('data-theme')) {
  document.documentElement.setAttribute('data-theme', initial);
}

export const useTheme = create<ThemeState>((set, get) => ({
  mode: initial,
  manual: stored === 'light' || stored === 'dark',
  setMode: (m, manual = true) => {
    applyTheme(m);
    if (manual) {
      try { localStorage.setItem(KEY, m); } catch { /* 隐私模式等场景忽略 */ }
    }
    set({ mode: m, manual: manual ? true : get().manual });
  },
  toggle: () => get().setMode(get().mode === 'dark' ? 'light' : 'dark'),
  syncRole: role => {
    if (get().manual) return;
    get().setMode(role === 'user' ? 'light' : 'dark', false);
  },
}));
