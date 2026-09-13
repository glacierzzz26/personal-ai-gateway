import { create } from 'zustand';
import type { PriceCurrency } from '@/types';

/* ============================================================
   计价币种 —— 全站价格的展示单位,由「系统设置 → 计价币种」决定。

   为什么要一个 store 而不是普通常量:后端把 计价币种 存在 settings 里
   (`display_currency`,默认 CNY),管理员会话加载设置后再水合到这里。
   fmt.price / fmt.usd 以 getState() 读取,设置变更时由 App 订阅触发整树重渲染
   —— 调用点(19+ 处)无需逐个传参或订阅。

   ⚠️ 非管理员会话不得请求 /settings(接口仅管理员可用,401 会被
   setUnauthorizedHandler 直接登出),故只在 admin 会话下水合;默认 CNY。
   ============================================================ */
interface CurrencyState {
  currency: PriceCurrency;
  setCurrency: (c: PriceCurrency) => void;
}

export const useCurrency = create<CurrencyState>(set => ({
  currency: 'CNY',
  setCurrency: currency => set({ currency }),
}));

/** 供非 React 代码(格式化函数)读取当前币种。 */
export const currentCurrency = (): PriceCurrency => useCurrency.getState().currency;
