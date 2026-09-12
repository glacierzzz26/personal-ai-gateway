import { create } from 'zustand';

const KEY_COLLAPSE = 'gw-collapsed';

interface UiState {
  collapsed: boolean;
  toggleCollapsed: () => void;
  /** Ctrl+K 命令面板开关（顶栏按钮与快捷键共用） */
  cmdkOpen: boolean;
  setCmdkOpen: (v: boolean) => void;
}

export const useUi = create<UiState>(set => ({
  collapsed: localStorage.getItem(KEY_COLLAPSE) === '1',

  toggleCollapsed: () => {
    set(s => {
      const next = !s.collapsed;
      localStorage.setItem(KEY_COLLAPSE, next ? '1' : '0');
      return { collapsed: next };
    });
  },

  cmdkOpen: false,
  setCmdkOpen: v => set({ cmdkOpen: v }),
}));
