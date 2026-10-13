import ReactEChartsCore from 'echarts-for-react/lib/core';
import * as echarts from 'echarts/core';
import { BarChart, LineChart } from 'echarts/charts';
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import type { EChartsOption } from 'echarts';

echarts.use([LineChart, BarChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer]);

interface Props {
  option: EChartsOption;
  height?: number;
}

/** 单一配色（无深色模式），无需按主题重建实例。 */
export default function Chart({ option, height = 280 }: Props) {
  return (
    <ReactEChartsCore
      echarts={echarts}
      option={option}
      notMerge
      lazyUpdate
      style={{ height, width: '100%' }}
      opts={{ renderer: 'canvas' }}
    />
  );
}
