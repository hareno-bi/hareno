import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { BarChart, GaugeChart, HeatmapChart, LineChart, PieChart, ScatterChart } from 'echarts/charts'
import { DatasetComponent, GridComponent, LegendComponent, TooltipComponent, VisualMapComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { echartsTheme, type ChartStyle } from './theme'

echarts.use([BarChart, GaugeChart, HeatmapChart, LineChart, PieChart, ScatterChart, DatasetComponent, GridComponent,
  LegendComponent, TooltipComponent, VisualMapComponent, CanvasRenderer])

// Chart renders one ECharts option. It re-creates the instance when the theme changes (ECharts
// themes are fixed at init) and follows its container's size. theme is any key that changes with it.
export function Chart({ option, theme, chart: cs, label }: { option: echarts.EChartsCoreOption; theme: string; chart: ChartStyle; label: string }) {
  const el = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts>(null)
  useEffect(() => {
    const c = echarts.init(el.current!, echartsTheme(cs), { renderer: 'canvas' })
    chart.current = c
    const ro = new ResizeObserver(() => c.resize())
    ro.observe(el.current!)
    return () => {
      ro.disconnect()
      c.dispose()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- theme is the key for cs and the tokens
  }, [theme])
  useEffect(() => {
    chart.current?.setOption(option, true)
  }, [option, theme])
  return <div ref={el} className="chart" role="img" aria-label={label} />
}
