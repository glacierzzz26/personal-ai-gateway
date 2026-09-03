import { create } from 'zustand';

export type ThemeMode = 'light' | 'dark';

const KEY_THEME = 'gw-theme';
const KEY_COLLAPSE = 'gw-collapsed';

function initialTheme(): ThemeMode {
  const saved = localStorage.getItem(KEY_THEME);
  if (saved === 'light' || saved === 'dark') return saved;
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

interface UiState {
  theme: ThemeMode;
  collapsed: boolean;
  setTheme: (t: ThemeMode) => void;
  toggleTheme: () => void;
  toggleCollapsed: () => void;
}

export const useUi = create<UiState>((set, get) => ({
  theme: initialTheme(),
  collapsed: localStorage.getItem(KEY_COLLAPSE) === '1',

  setTheme: (t) => {
    localStorage.setItem(KEY_THEME, t);
    document.documentElement.dataset.theme = t;
    set({ theme: t });
  },
  toggleTheme: () => get().setTheme(get().theme === 'dark' ? 'light' : 'dark'),
  toggleCollapsed: () => {
    const next = !get().collapsed;
    localStorage.setItem(KEY_COLLAPSE, next ? '1' : '0');
    set({ collapsed: next });
  },
}));
