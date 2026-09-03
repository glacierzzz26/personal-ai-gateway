import { create } from 'zustand';
import type { AdminMe } from '@/types';

interface SessionState {
  admin: AdminMe | null;
  setAdmin: (a: AdminMe | null) => void;
}

/** 当前登录管理员。null=未登录(SessionGate 显示登录页)。 */
export const useSession = create<SessionState>(set => ({
  admin: null,
  setAdmin: a => set({ admin: a }),
}));
