import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { BarChart, HeatmapChart, LineChart, PieChart, ScatterChart } from 'echarts/charts'
import { DatasetComponent, GridComponent, LegendComponent, TooltipComponent, VisualMapComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { echartsTheme, type Mode } from './theme'

echarts.use([BarChart, HeatmapChart, LineChart, PieChart, ScatterChart, DatasetComponent, GridComponent,
  LegendComponent, TooltipComponent, VisualMapComponent, CanvasRenderer])

// Chart renders one ECharts option. It re-creates the instance when the theme changes (ECharts
// themes are fixed at init) and follows its container's size.
export function Chart({ option, mode, label }: { option: echarts.EChartsCoreOption; mode: Mode; label: string }) {
  const el = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts>(null)
  useEffect(() => {
    const c = echarts.init(el.current!, echartsTheme(), { renderer: 'canvas' })
    chart.current = c
    const ro = new ResizeObserver(() => c.resize())
    ro.observe(el.current!)
    return () => {
      ro.disconnect()
      c.dispose()
    }
  }, [mode])
  useEffect(() => {
    chart.current?.setOption(option, true)
  }, [option, mode])
  return <div ref={el} className="chart" role="img" aria-label={label} />
}
