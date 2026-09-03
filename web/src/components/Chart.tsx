import { useEffect, useRef } from 'react'
import { init, type ECharts, type EChartsOption } from 'echarts'

export interface ChartProps {
  option: EChartsOption
  height?: number
  className?: string
}

// ECharts 薄封装:初始化一次、Responsive resize、option 变化即 setOption。
export default function Chart({ option, height = 300, className }: ChartProps) {
  const elRef = useRef<HTMLDivElement>(null)
  const instRef = useRef<ECharts | null>(null)

  useEffect(() => {
    const el = elRef.current
    if (!el) return
    const inst = init(el)
    instRef.current = inst
    const ro = new ResizeObserver(() => inst.resize())
    ro.observe(el)
    return () => {
      ro.disconnect()
      inst.dispose()
      instRef.current = null
    }
  }, [])

  useEffect(() => {
    instRef.current?.setOption(option, { notMerge: true })
  }, [option])

  return <div ref={elRef} className={className} style={{ width: '100%', height }} />
}
