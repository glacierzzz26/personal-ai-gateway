import { theme, type ThemeConfig } from 'antd';

/**
 * 设计令牌 —— 唯一色值来源。
 *
 * 主色:靛蓝 Indigo(线性/Stripe 一派的现代 SaaS 观感),鲜亮而不刺眼。
 * 规范外 hex 一律禁止;需要更浅/更透的层次时只用 rgba() 从主色派生。
 * 圆角四档:卡片 8 / 按钮 6 / 徽章 4 / 弹层 12。禁止深色模式。
 */
export const TOKENS = {
  bg: '#F8FAFC',
  card: '#FFFFFF',
  border: '#E2E8F0',

  primary: '#4F46E5',
  primaryHover: '#4338CA',
  primary50: '#EEF2FF',
  primary100: '#C7D2FE',
  /* 主色浅一档 —— 同一指标的两系列对比(输入价/输出价)用同色相深浅,不同指标才换色相 */
  primarySoft: '#A5B4FC',

  /* 图表分类色板 —— 多色相,系列间一眼可分(靛蓝/青/紫/琥珀/玫红/翠绿)。
     语义线(错误=err、花费=warn)仍走语义色,不占色板位。 */
  c1: '#4F46E5',
  c2: '#06B6D4',
  c3: '#8B5CF6',
  c4: '#F59E0B',
  c5: '#EC4899',
  c6: '#10B981',

  title: '#0F172A',
  text: '#475569',
  aux: '#94A3B8',

  ok: '#10B981',
  warn: '#F59E0B',
  err: '#EF4444',

  rCard: 8,
  rBtn: 6,
  rBadge: 4,
  rPopup: 12,
} as const;

/** 控件统一高度（对齐按钮 36px） */
export const CONTROL_HEIGHT = 36;

const components: NonNullable<ThemeConfig['components']> = {
  Card: {
    borderRadiusLG: TOKENS.rCard,
    paddingLG: 20,
    boxShadowTertiary: 'none',
    headerHeight: 52,
  },
  Table: {
    headerBg: TOKENS.bg,
    headerSplitColor: 'transparent',
    headerColor: TOKENS.aux,
    rowHoverBg: TOKENS.bg,
    borderColor: TOKENS.border,
    cellPaddingBlock: 13,
    cellPaddingInline: 12,
    cellPaddingBlockSM: 8,
    cellPaddingInlineSM: 10,
    fontSize: 14,
  },
  Button: {
    borderRadius: TOKENS.rBtn,
    fontWeight: 500,
    primaryShadow: 'none',
    defaultShadow: 'none',
    paddingInline: 14,
  },
  Input: {
    borderRadius: TOKENS.rBtn,
    activeShadow: '0 0 0 3px rgba(79,70,229,0.12)',
    hoverBorderColor: TOKENS.aux,
    activeBorderColor: TOKENS.primary,
  },
  Select: {
    borderRadius: TOKENS.rBtn,
    optionSelectedBg: TOKENS.primary50,
    optionSelectedColor: TOKENS.primary,
    optionActiveBg: TOKENS.bg,
  },
  Tag: {
    borderRadiusSM: TOKENS.rBadge,
    defaultBg: TOKENS.card,
    defaultColor: TOKENS.text,
  },
  Modal: { borderRadiusLG: TOKENS.rPopup, titleFontSize: 17 },
  Drawer: { borderRadiusLG: TOKENS.rPopup, footerPaddingBlock: 15 },
  Menu: {
    itemBorderRadius: TOKENS.rBtn,
    itemHeight: 42,
    itemMarginInline: 9,
    itemPaddingInline: 14,
    itemColor: TOKENS.text,
    itemSelectedColor: TOKENS.primary,
    itemSelectedBg: TOKENS.primary50,
    itemHoverBg: TOKENS.bg,
    itemActiveBg: TOKENS.primary50,
    subMenuItemBg: 'transparent',
    groupTitleFontSize: 12,
    groupTitleColor: TOKENS.aux,
  },
  Statistic: { contentFontSize: 30, titleFontSize: 13 },
  Tabs: {
    horizontalItemPadding: '13px 0',
    horizontalItemGutter: 22,
    cardBg: 'transparent',
    itemSelectedColor: TOKENS.primary,
    inkBarColor: TOKENS.primary,
  },
  Segmented: {
    itemSelectedBg: TOKENS.card,
    itemSelectedColor: TOKENS.title,
    trackBg: TOKENS.bg,
    borderRadius: TOKENS.rBtn,
    itemColor: TOKENS.text,
  },
  Slider: { handleSize: 8, handleSizeHover: 8, trackBg: TOKENS.primary100, trackHoverBg: TOKENS.primary },
  /* 开关开启态用主色（与原型一致），不用绿色 */
  Switch: { colorPrimary: TOKENS.primary },
  Progress: { defaultColor: TOKENS.primary, remainingColor: TOKENS.bg },
  Descriptions: { labelBg: TOKENS.bg },
  Alert: { borderRadiusLG: TOKENS.rCard },
  Tooltip: { borderRadius: TOKENS.rPopup, colorBgSpotlight: TOKENS.title },
  Dropdown: { borderRadiusLG: TOKENS.rPopup },
  Empty: { colorTextDescription: TOKENS.aux },
};

export const lightTheme: ThemeConfig = {
  algorithm: theme.defaultAlgorithm,
  token: {
    colorPrimary: TOKENS.primary,
    colorPrimaryHover: TOKENS.primaryHover,
    colorPrimaryBg: TOKENS.primary50,
    colorPrimaryBorder: TOKENS.primary100,
    colorLink: TOKENS.primary,
    colorLinkHover: TOKENS.primaryHover,

    colorSuccess: TOKENS.ok,
    colorWarning: TOKENS.warn,
    colorError: TOKENS.err,
    colorInfo: TOKENS.c1,

    colorTextBase: TOKENS.title,
    colorText: TOKENS.text,
    colorTextHeading: TOKENS.title,
    colorTextSecondary: TOKENS.text,
    colorTextTertiary: TOKENS.aux,
    colorTextQuaternary: TOKENS.aux,

    colorBgLayout: TOKENS.bg,
    colorBgContainer: TOKENS.card,
    colorBorder: TOKENS.border,
    colorBorderSecondary: TOKENS.border,

    borderRadius: TOKENS.rBtn,
    borderRadiusLG: TOKENS.rCard,
    borderRadiusSM: TOKENS.rBadge,
    borderRadiusXS: TOKENS.rBadge,

    controlHeight: CONTROL_HEIGHT,
    fontSize: 14,
    fontFamily:
      "'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', system-ui, sans-serif",
    wireframe: false,
    motionDurationMid: '160ms',
    boxShadow: 'none',
    boxShadowSecondary: 'none',
    boxShadowTertiary: 'none',
  },
  components,
};
