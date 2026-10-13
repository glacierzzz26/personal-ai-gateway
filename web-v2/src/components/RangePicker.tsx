import dayjs, { type Dayjs } from 'dayjs';
import { useState } from 'react';
import { Seg } from '@/ui/primitives';
import { toast } from '@/ui/toast';
import type { StatRangeQuery } from '@/types';
import { STAT_MAX_DAYS } from '@/types';

/* 预设与自定义共用一套「本地自然日」口径:
   预设 N 天 = 最近 N 个自然日(含今天) -> 提交 { days: N };
   手选区间     = [起, 止] 含首尾 -> 提交 { from, to }。
   为让选中态直观,若手选区间的起止正好等于某个预设窗口,就回退成预设。 */

export const PRESETS = [1, 7, 30] as const;
export type PresetDays = (typeof PRESETS)[number];

export interface SelectedRange {
  /** 命中的预设天数;自定义区间为 null */
  days: PresetDays | null;
  /** 自定义区间(含首尾),命中预设时为 null */
  from: Dayjs | null;
  to: Dayjs | null;
}

const PRESET_SET = new Set<number>(PRESETS);

/** "近 N 天(含今天)" 的本地自然日起止。 */
export function presetBounds(n: number): [Dayjs, Dayjs] {
  const end = dayjs().startOf('day');
  return [end.subtract(n - 1, 'day'), end];
}

/** 一个区间是否恰好等于某个预设窗口(首尾都落在自然日边界上)。 */
function snapPreset(from: Dayjs, to: Dayjs): PresetDays | null {
  const end = dayjs().startOf('day');
  const a = from.startOf('day');
  const b = to.startOf('day');
  if (!b.isSame(end, 'day')) return null;
  const n = b.diff(a, 'day') + 1;
  return PRESET_SET.has(n) && n <= STAT_MAX_DAYS ? (n as PresetDays) : null;
}

/**
 * 默认窗口 = 最近 1 天(issue #13)。
 *
 * 站主看的是「这门生意今天怎么样」,靠的是每天的节奏 —— 默认 7 天会把一次上游抖动、
 * 一个客户放量摊薄到看不见。1 天窗口同时落在小时桶(days<=3 → bucket=hour),
 * 正好回答「今天几点开始不对劲」。7/30 天退为对照视图,预设里仍可选。
 */
export const defaultRange = (): SelectedRange => {
  const [from, to] = presetBounds(1);
  return { days: 1, from, to };
};

/** 选中态 -> 后端查询参数。 */
export function toQuery(r: SelectedRange): StatRangeQuery {
  if (r.days) return { days: r.days };
  if (r.from && r.to) return { from: r.from.format('YYYY-MM-DD'), to: r.to.format('YYYY-MM-DD') };
  return { days: 1 };
}

/** 副标题用的可读描述。 */
export function rangeLabel(r: SelectedRange): string {
  if (r.days) return `近 ${r.days} 天`;
  if (r.from && r.to) return `${r.from.format('MM-DD')} ~ ${r.to.format('MM-DD')}`;
  return '近 1 天';
}

/** 紧邻当前窗口之前的等长窗口 —— 首页环比(较上期)用。 */
export function previousWindow(r: SelectedRange): StatRangeQuery {
  const [f, t] = r.days ? presetBounds(r.days) : [r.from, r.to];
  if (!f || !t) return { days: 1 };
  const end = t.startOf('day');
  const n = end.diff(f.startOf('day'), 'day') + 1;
  const prevTo = f.startOf('day').subtract(1, 'day');
  return { from: prevTo.subtract(n - 1, 'day').format('YYYY-MM-DD'), to: prevTo.format('YYYY-MM-DD') };
}

/**
 * 统计窗口选择器:1天/7天/30天 预设 + 自定义日期区间(≤90 天,日志保留上限)。
 * 受控组件 —— 选中态与提交态都由父级持有,便于图表副标题同步。
 * UI 对齐原型 .seg；自定义区间用两个原生 date 输入(不引 antd DatePicker)。
 */
export default function RangePicker({
  value,
  onChange,
  size,
}: {
  value: SelectedRange;
  onChange: (r: SelectedRange) => void;
  size?: 'sm';
}) {
  const active: PresetDays | 'custom' = value.days ?? 'custom';
  const [customOpen, setCustomOpen] = useState(false);
  const [cf, setCf] = useState(() => (value.from ? value.from.format('YYYY-MM-DD') : presetBounds(1)[0].format('YYYY-MM-DD')));
  const [ct, setCt] = useState(() => (value.to ? value.to.format('YYYY-MM-DD') : presetBounds(1)[1].format('YYYY-MM-DD')));

  const pickPreset = (n: number) => {
    const [from, to] = presetBounds(n);
    setCustomOpen(false);
    onChange({ days: n as PresetDays, from, to });
  };

  const applyCustom = (fromStr: string, toStr: string) => {
    if (!fromStr || !toStr) return;
    const from = dayjs(fromStr).startOf('day');
    const to = dayjs(toStr).startOf('day');
    if (from.isAfter(to)) {
      toast('起始日期不能晚于结束日期', 'err');
      return;
    }
    if (to.isAfter(dayjs(), 'day')) {
      toast('结束日期不能晚于今天', 'err');
      return;
    }
    if (to.diff(from, 'day') + 1 > STAT_MAX_DAYS) {
      toast(`自定义区间最长 ${STAT_MAX_DAYS} 天`, 'err');
      return;
    }
    const hit = snapPreset(from, to);
    if (hit) {
      pickPreset(hit);
      return;
    }
    onChange({ days: null, from, to });
  };

  return (
    <div className="hstack wrap" style={{ gap: 8 }}>
      <Seg
        size={size}
        value={active}
        onChange={(v) => {
          if (v === 'custom') {
            setCustomOpen(true);
            applyCustom(cf, ct);
          } else {
            pickPreset(Number(v));
          }
        }}
        options={[
          ...PRESETS.map((d) => ({ value: d, label: `近 ${d} 天` })),
          { value: 'custom' as const, label: '自定义' },
        ]}
      />
      {customOpen && (
        <div className="hstack" style={{ gap: 5 }}>
          <input
            type="date"
            className="input"
            style={{ width: 150 }}
            value={cf}
            max={ct}
            onChange={(e) => {
              setCf(e.target.value);
              applyCustom(e.target.value, ct);
            }}
          />
          <span className="muted small">~</span>
          <input
            type="date"
            className="input"
            style={{ width: 150 }}
            value={ct}
            min={cf}
            onChange={(e) => {
              setCt(e.target.value);
              applyCustom(cf, e.target.value);
            }}
          />
        </div>
      )}
    </div>
  );
}
