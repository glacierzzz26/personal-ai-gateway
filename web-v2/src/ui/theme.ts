import { create } from 'zustand';

export type ThemeMode = 'dark' | 'light';

/** 原型口径：localStorage['gw-theme'] 是该键的唯一真源。 */
function readStored(): ThemeMode | null {
  try {
    const v = localStorage.getItem('gw-theme');
    return v === 'light' ? 'light' : v === 'dark' ? 'dark' : null;
  } catch {
    return null;
  }
}

function applyAttr(mode: ThemeMode) {
  document.documentElement.setAttribute('data-theme', mode);
}

interface ThemeState {
  mode: ThemeMode;
  /** 用户是否手动切过主题（切过则不再被角色默认覆盖）。 */
  override: boolean;
  /** 显式设主题；byUser=true 时写入 localStorage（成为持久偏好）。 */
  setMode: (mode: ThemeMode, byUser?: boolean) => void;
  toggle: () => void;
  /** 会话恢复后按角色取默认主题 —— 仅在用户从未手动指定过时生效。 */
  applyRoleDefault: (isAdmin: boolean) => void;
}

const initial = readStored() ?? 'dark';

export const useTheme = create<ThemeState>((set, get) => ({
  mode: initial,
  override: readStored() != null,
  setMode: (mode, byUser) => {
    applyAttr(mode);
    if (byUser) {
      try {
        localStorage.setItem('gw-theme', mode);
      } catch {
        /* ignore */
      }
    }
    set({ mode, override: byUser ? true : get().override });
  },
  toggle: () => {
    const next: ThemeMode = get().mode === 'dark' ? 'light' : 'dark';
    get().setMode(next, true);
  },
  applyRoleDefault: (isAdmin) => {
    if (get().override) return;
    get().setMode(isAdmin ? 'dark' : 'light', false);
  },
}));

/** 首帧前对齐 DOM（index.html 内联脚本已先行设过，这里是 React 侧的兜底）。 */
export function initTheme() {
  applyAttr(useTheme.getState().mode);
}
