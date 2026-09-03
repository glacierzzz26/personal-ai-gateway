import { theme, type ThemeConfig } from 'antd';

/** 圆角只用 6 / 8 / 10 / 12 四档，主色只出现在按钮、选中态、图表主系列 */
const components: NonNullable<ThemeConfig['components']> = {
  Card: {
    borderRadiusLG: 10, paddingLG: 20, boxShadowTertiary: 'none',
    headerHeight: 48,
  },
  Table: {
    headerBg: 'transparent', headerSplitColor: 'transparent',
    headerColor: '#64748B',
    rowHoverBg: '#F1F5F9', borderColor: '#EFF1F4',
    cellPaddingBlock: 10, cellPaddingInline: 14,
    cellPaddingBlockSM: 6, cellPaddingInlineSM: 10,
  },
  Button: {
    borderRadius: 8, fontWeight: 500,
    primaryShadow: 'none', defaultShadow: 'none', paddingInline: 14,
  },
  Input: { borderRadius: 8, activeShadow: '0 0 0 3px rgba(37,99,235,0.10)', hoverBorderColor: '#94A3B8' },
  Select: { borderRadius: 8, optionSelectedBg: '#EFF6FF' },
  Tag: { borderRadiusSM: 6, defaultBg: '#F1F5F9', defaultColor: '#475569' },
  Modal: { borderRadiusLG: 12, titleFontSize: 16 },
  Drawer: { borderRadiusLG: 12, footerPaddingBlock: 12 },
  Menu: {
    itemBorderRadius: 8, itemHeight: 38, itemMarginInline: 8, itemPaddingInline: 12,
    itemColor: '#475569', itemSelectedColor: '#2563EB', itemSelectedBg: '#EFF6FF',
    itemHoverBg: '#F1F5F9', itemActiveBg: '#EFF6FF', subMenuItemBg: 'transparent',
    groupTitleFontSize: 12,
  },
  Statistic: { contentFontSize: 24, titleFontSize: 13 },
  Tabs: { horizontalItemPadding: '10px 0', horizontalItemGutter: 24, cardBg: 'transparent', itemSelectedColor: '#2563EB' },
  Segmented: { itemSelectedBg: '#FFFFFF', trackBg: '#F1F5F9', borderRadius: 8 },
  Slider: { handleSize: 8, handleSizeHover: 8 },
};

export const lightTheme: ThemeConfig = {
  algorithm: theme.defaultAlgorithm,
  token: {
    colorPrimary: '#2563EB',
    colorSuccess: '#16A34A', colorWarning: '#F59E0B',
    colorError: '#EF4444', colorInfo: '#3B82F6',
    colorTextBase: '#0F172A',
    colorTextSecondary: '#475569',
    colorTextTertiary: '#94A3B8',
    colorBgLayout: '#F7F8FA',
    colorBgContainer: '#FFFFFF',
    colorBorder: '#E5E7EB',
    colorBorderSecondary: '#EFF1F4',
    borderRadius: 8, borderRadiusLG: 10, borderRadiusSM: 6,
    controlHeight: 34, fontSize: 14,
    fontFamily: 'Inter, -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif',
    wireframe: false, motionDurationMid: '160ms',
  },
  components,
};

export const darkTheme: ThemeConfig = {
  ...lightTheme,
  algorithm: theme.darkAlgorithm,
  token: {
    ...lightTheme.token,
    colorPrimary: '#3B82F6',
    colorTextBase: '#E2E8F0',
    colorTextSecondary: '#94A3B8',
    colorTextTertiary: '#64748B',
    colorBgLayout: '#0B0E14',
    colorBgContainer: '#131722',
    colorBorder: '#232936',
    colorBorderSecondary: '#1B2130',
  },
  components: {
    ...components,
    Table: { ...components.Table, rowHoverBg: '#1A2030', borderColor: '#1B2130', headerColor: '#64748B' },
    Tag: { ...components.Tag, defaultBg: '#1A2030', defaultColor: '#94A3B8' },
    Menu: {
      ...components.Menu, itemColor: '#94A3B8', itemSelectedColor: '#3B82F6',
      itemSelectedBg: '#14243D', itemHoverBg: '#1A2030', itemActiveBg: '#14243D',
    },
    Segmented: { ...components.Segmented, itemSelectedBg: '#1F2839', trackBg: '#1A2030' },
  },
};
