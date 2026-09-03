import ReactEChartsCore from 'echarts-for-react/lib/core';
import * as echarts from 'echarts/core';
import { BarChart, LineChart } from 'echarts/charts';
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import type { EChartsOption } from 'echarts';
import { useUi } from '@/stores/ui';

echarts.use([LineChart, BarChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer]);

interface Props {
  option: EChartsOption;
  height?: number;
}

/** 主题切换时用 key 强制重建实例，避免 canvas 残留旧配色 */
export default function Chart({ option, height = 280 }: Props) {
  const theme = useUi(s => s.theme);
  return (
    <ReactEChartsCore
      key={theme}
      echarts={echarts}
      option={option}
      notMerge
      lazyUpdate
      style={{ height, width: '100%' }}
      opts={{ renderer: 'canvas' }}
    />
  );
}
