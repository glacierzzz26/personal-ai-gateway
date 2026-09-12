import type { ReactNode } from 'react';
import {
  IconChannels, IconDashboard, IconLogs, IconModels, IconRouting, IconSettings, IconTokens, IconUsers,
} from '@/components/icons';

export interface NavItem {
  key: string;
  label: string;
  group: string;
  icon: ReactNode;
  /** 仅管理员可见 */
  adminOnly: boolean;
}

/**
 * 导航表 —— 侧栏、面包屑、Ctrl+K 三处共用一份，避免三处各写一遍导致漂移。
 * 顺序即侧栏顺序；分组名同时是面包屑的第一段。
 */
export const NAV_ITEMS: NavItem[] = [
  { key: '/dashboard', label: '运行总览', group: '概览', icon: <IconDashboard />, adminOnly: true },
  { key: '/channels', label: '渠道管理', group: '资源', icon: <IconChannels />, adminOnly: true },
  { key: '/models', label: '模型广场', group: '资源', icon: <IconModels />, adminOnly: true },
  { key: '/routing', label: '路由规则', group: '资源', icon: <IconRouting />, adminOnly: true },
  { key: '/tokens', label: '访问令牌', group: '访问', icon: <IconTokens />, adminOnly: false },
  { key: '/logs', label: '请求日志', group: '观测', icon: <IconLogs />, adminOnly: true },
  { key: '/users', label: '用户管理', group: '系统', icon: <IconUsers />, adminOnly: true },
  { key: '/settings', label: '系统设置', group: '系统', icon: <IconSettings />, adminOnly: true },
];

/** 侧栏分组顺序 */
export const NAV_GROUPS = ['概览', '资源', '访问', '观测', '系统'] as const;

export const visibleNav = (isAdmin: boolean): NavItem[] =>
  NAV_ITEMS.filter(it => isAdmin || !it.adminOnly);

/** 路径 → 面包屑两段；未命中时回退到原路径 */
export function crumbOf(pathname: string, isAdmin: boolean): { group: string; label: string } {
  const hit = visibleNav(isAdmin).find(it => it.key === pathname);
  if (hit) return { group: hit.group, label: hit.label };
  return { group: '', label: pathname };
}
