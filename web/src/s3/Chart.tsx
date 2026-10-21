import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { BarChart, BoxplotChart, CandlestickChart, ChordChart, FunnelChart, GaugeChart, HeatmapChart, LineChart, MapChart, PieChart, RadarChart, SankeyChart, ScatterChart, SunburstChart, TreemapChart } from 'echarts/charts'
import { CalendarComponent, DatasetComponent, GeoComponent, TitleComponent, GridComponent, LegendComponent, TooltipComponent, RadarComponent, VisualMapComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { echartsTheme, type ChartStyle, type Tokens } from './theme'

echarts.use([MapChart, GeoComponent, TitleComponent, BarChart, BoxplotChart, CandlestickChart, ChordChart, FunnelChart, RadarChart, SankeyChart, SunburstChart, CalendarComponent, RadarComponent, GaugeChart, HeatmapChart, LineChart, PieChart, ScatterChart, TreemapChart, DatasetComponent, GridComponent,
  LegendComponent, TooltipComponent, VisualMapComponent, CanvasRenderer])

// Chart renders one ECharts option. It re-creates the instance when the theme changes (ECharts
// themes are fixed at init) and follows its container's size. theme is any key that changes with it.
// onPick gets the category (X value, or slice) of a clicked bar, point or slice.
export function Chart({ option, theme, chart: cs, tokens, label, onPick }: {
  option: echarts.EChartsCoreOption; theme: string; chart: ChartStyle; tokens: Tokens; label: string; onPick?: (name: string) => void
}) {
  const el = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts>(null)
  const pick = useRef(onPick)
  useEffect(() => { pick.current = onPick })
  useEffect(() => {
    const c = echarts.init(el.current!, echartsTheme(cs, tokens), { renderer: 'canvas' })
    chart.current = c
    c.on('click', (p) => pick.current?.(String(p.name)))
    // a click elsewhere in a category's band picks it too: thin bars are hard to hit
    c.getZr().on('click', (e) => {
      if (e.target || !pick.current || !c.containPixel('grid', [e.offsetX, e.offsetY])) return
      type Axis = { type?: string; data?: unknown[] }[] | undefined
      const o = c.getOption() as { xAxis?: Axis; yAxis?: Axis }
      const at = c.convertFromPixel({ gridIndex: 0 }, [e.offsetX, e.offsetY]) as number[]
      const cat = o.xAxis?.[0]?.type === 'category' ? o.xAxis[0].data?.[Math.round(at[0])]
        : o.yAxis?.[0]?.type === 'category' ? o.yAxis[0].data?.[Math.round(at[1])] : undefined
      if (cat !== undefined) pick.current(String(cat))
    })
    const ro = new ResizeObserver(() => c.resize())
    ro.observe(el.current!)
    return () => {
      ro.disconnect()
      c.dispose()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- theme is the key for cs and tokens
  }, [theme])
  useEffect(() => {
    chart.current?.setOption(option, true)
  }, [option, theme])
  return <div ref={el} className="chart" role="img" aria-label={label} />
}
