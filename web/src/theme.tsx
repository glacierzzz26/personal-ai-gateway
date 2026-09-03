import { createContext, useContext } from 'react'
import type { ThemeConfig } from 'antd'
import { theme as antdTheme } from 'antd'

export type ThemeMode = 'light' | 'dark'

const STORAGE_KEY = 'gw.theme'

export function initialThemeMode(): ThemeMode {
  try {
    const s = localStorage.getItem(STORAGE_KEY)
    if (s === 'light' || s === 'dark') return s
    if (window.matchMedia?.('(prefers-color-scheme: dark)').matches) return 'dark'
  } catch {
    /* ignore */
  }
  return 'light'
}

export function themeConfig(mode: ThemeMode): ThemeConfig {
  const dark = mode === 'dark'
  return {
    algorithm: dark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
    token: {
      colorPrimary: dark ? '#7a89f5' : '#4c5fe0',
      borderRadius: 8,
      fontSize: 14,
    },
  }
}

export function persistThemeMode(mode: ThemeMode) {
  try {
    localStorage.setItem(STORAGE_KEY, mode)
  } catch {
    /* ignore */
  }
}

export interface ThemeCtxValue {
  mode: ThemeMode
  dark: boolean
  toggle: () => void
}

export const ThemeContext = createContext<ThemeCtxValue>({
  mode: 'light',
  dark: false,
  toggle: () => {},
})

export function useTheme(): ThemeCtxValue {
  return useContext(ThemeContext)
}
