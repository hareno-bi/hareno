import { useEffect, useMemo, useState, type ReactNode } from 'react'
import type { EChartsCoreOption } from 'echarts/core'
import { Chart } from './Chart'
import { fmtCompact, fmtDelta, fmtPct, fmtWhole } from './format'
import { token, type Mode } from './theme'

type Series = { name: string; data: number[] }
type Period = { revenue: number; orders: number; margin: number; aov: number }
type Data = {
  months: string[]
  byRegion: Series[]
  byCategory: Series[]
  monthly: { revenue: number; margin: number }[]
  kpi: { current: Period; prior: Period }
  customers: [number, number, string, string][]
}

const sum = (a: number[]) => a.reduce((x, y) => x + y, 0)
const monthLabel = (m: string) => new Date(m + '-01T00:00:00').toLocaleDateString('en', { month: 'short', year: '2-digit' })
const axisCompact = { axisLabel: { formatter: (v: number) => fmtCompact(v) } }
const tooltipAxis = { trigger: 'axis', valueFormatter: (v: number) => fmtWhole(v) }

export default function Charts({ mode }: { mode: Mode }) {
  const [data, setData] = useState<Data | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    fetch(import.meta.env.BASE_URL + 's3/charts.json')
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then(setData, (e: Error) => setError(e.message))
  }, [])
  const opts = useMemo(() => (data ? options(data) : null), [data, mode]) // mode: options read colour tokens

  if (error) return <p className="note">Sample data missing ({error}). Run <code>web/scripts/s3-snapshot.sh</code>.</p>
  if (!data || !opts) return <div className="grid">{[1, 2, 3, 4].map((i) => <Card key={i} title="" state="loading" />)}</div>
  const { current: cur, prior } = data.kpi
  return (
    <>
      <p className="note">
        Sample set for sign-off. Synthetic sales computed by the dev mart (12,000 customers, 24 months); amounts carry
        no currency until one is agreed. Every chart supports light and dark mode (top right), hover tooltips and
        resizing.
      </p>
      <section className="kpis" aria-label="Key figures, last 12 months">
        <Kpi label="Revenue" value={fmtCompact(cur.revenue)} delta={cur.revenue / prior.revenue - 1} />
        <Kpi label="Orders" value={fmtWhole(cur.orders)} delta={cur.orders / prior.orders - 1} />
        <Kpi label="Average order" value={fmtWhole(cur.aov)} delta={cur.aov / prior.aov - 1} />
        <Kpi label="Margin" value={fmtPct(cur.margin)} delta={cur.margin - prior.margin} points />
      </section>
      <div className="grid">
        <Card title="Revenue by region" sub="Monthly, line" wide>
          <Chart mode={mode} option={opts.line} label="Line chart: monthly revenue by region" />
        </Card>
        <Card title="Revenue share by region" sub="24 months, donut">
          <Chart mode={mode} option={opts.donut} label="Donut chart: revenue share by region" />
        </Card>
        <Card title="Revenue by category" sub="Monthly, stacked column" wide>
          <Chart mode={mode} option={opts.stacked} label="Stacked column chart: monthly revenue by category" />
        </Card>
        <Card title="Revenue by category" sub="24 months, bar">
          <Chart mode={mode} option={opts.bar} label="Bar chart: revenue by category" />
        </Card>
        <Card title="Revenue and margin" sub="Monthly, combined bar and line, two axes" wide stale="Data as of 09:41 · refreshing">
          <Chart mode={mode} option={opts.combo} label="Combined chart: monthly revenue and margin" />
        </Card>
        <Card title="Top 400 customers" sub="Revenue against margin, scatter">
          <Chart mode={mode} option={opts.scatter} label="Scatter chart: customer revenue against margin" />
        </Card>
        <Card title="Revenue by region and month" sub="Heatmap" wide>
          <Chart mode={mode} option={opts.heatmap} label="Heatmap: revenue by region and month" />
        </Card>
      </div>
      <h2>States</h2>
      <div className="grid states">
        <Card title="Loading" state="loading" />
        <Card title="No data" state="empty" />
        <Card title="Query failed" state="error" />
        <Card title="Access removed" state="denied" />
      </div>
    </>
  )
}

function Kpi({ label, value, delta, points }: { label: string; value: string; delta: number; points?: boolean }) {
  const shown = points ? `${delta >= 0 ? '+' : '−'}${Math.abs(delta * 100).toFixed(1)} pts` : fmtDelta(delta)
  return (
    <div className="card kpi">
      <div className="kpi-label">{label}</div>
      <div className="kpi-value">{value}</div>
      <div className={'kpi-delta ' + (delta >= 0 ? 'up' : 'down')}>
        <span aria-hidden>{delta >= 0 ? '▲' : '▼'}</span> {shown} <span className="muted">vs prior 12 months</span>
      </div>
    </div>
  )
}

const stateText = {
  loading: 'Loading…',
  empty: 'No data for the current filters.',
  error: 'This chart could not be loaded. Other charts are unaffected. Retry',
  denied: 'You no longer have access to this report.',
}

function Card(props: {
  title: string
  sub?: string
  wide?: boolean
  stale?: string
  state?: keyof typeof stateText
  children?: ReactNode
}) {
  return (
    <section className={'card' + (props.wide ? ' wide' : '')} aria-busy={props.state === 'loading'}>
      <header>
        <h3>{props.title}</h3>
        {props.sub && <span className="muted">{props.sub}</span>}
        {props.stale && <span className="badge" title="Showing the last result while a refresh runs">{props.stale}</span>}
      </header>
      {props.state ? <div className={'state ' + props.state}>{stateText[props.state]}</div> : props.children}
    </section>
  )
}

function options(d: Data): Record<string, EChartsCoreOption> {
  const months = d.months.map(monthLabel)
  const grid = { left: 8, right: 16, top: 40, bottom: 8, containLabel: true }
  const legend = { top: 0, left: 0 }
  return {
    line: {
      grid, legend, tooltip: tooltipAxis,
      xAxis: { type: 'category', data: months, boundaryGap: false },
      yAxis: { type: 'value', ...axisCompact },
      series: d.byRegion.map((s) => ({ type: 'line', name: s.name, data: s.data, smooth: 0.2, symbol: 'none', lineStyle: { width: 2 } })),
    },
    donut: {
      legend: { bottom: 0 },
      tooltip: { trigger: 'item', valueFormatter: (v: number) => fmtWhole(v) },
      series: [{
        type: 'pie', radius: ['40%', '74%'], center: ['50%', '45%'], padAngle: 1,
        itemStyle: { borderRadius: 4 },
        label: { position: 'inside', color: token('--on-series'), textBorderWidth: 0, fontWeight: 600,
          formatter: (p: { percent: number }) => fmtPct(p.percent / 100) },
        data: d.byRegion.map((s) => ({ name: s.name, value: sum(s.data) })),
      }],
    },
    stacked: {
      grid, legend, tooltip: tooltipAxis,
      xAxis: { type: 'category', data: months },
      yAxis: { type: 'value', ...axisCompact },
      series: d.byCategory.map((s) => ({ type: 'bar', name: s.name, data: s.data, stack: 'total', barMaxWidth: 28 })),
    },
    bar: {
      grid: { ...grid, top: 8 },
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, valueFormatter: (v: number) => fmtWhole(v) },
      xAxis: { type: 'value', ...axisCompact },
      yAxis: { type: 'category', data: d.byCategory.map((s) => s.name), inverse: true },
      series: [{
        type: 'bar', data: d.byCategory.map((s) => sum(s.data)), barMaxWidth: 28,
        label: { show: true, position: 'insideRight', formatter: (p: { value: number }) => fmtCompact(p.value), color: token('--on-series'), textBorderWidth: 0 },
        itemStyle: { borderRadius: [0, 4, 4, 0] },
      }],
    },
    combo: {
      grid: { ...grid, top: 56 }, legend,
      tooltip: { trigger: 'axis' },
      xAxis: { type: 'category', data: months },
      yAxis: [
        { type: 'value', name: 'Revenue', ...axisCompact },
        { type: 'value', name: 'Margin', min: 0.3, max: 0.5, splitLine: { show: false }, axisLabel: { formatter: (v: number) => fmtPct(v) } },
      ],
      series: [
        { type: 'bar', name: 'Revenue', data: d.monthly.map((m) => m.revenue), barMaxWidth: 24, tooltip: { valueFormatter: (v: number) => fmtWhole(v) } },
        { type: 'line', name: 'Margin', yAxisIndex: 1, data: d.monthly.map((m) => m.margin), symbolSize: 6, tooltip: { valueFormatter: (v: number) => fmtPct(v) } },
      ],
    },
    scatter: {
      grid: { ...grid, top: 56, bottom: 28 }, legend,
      tooltip: {
        trigger: 'item',
        formatter: (p: { value: [number, number, string, string] }) =>
          `${p.value[2]} · ${p.value[3]}<br/>Revenue ${fmtWhole(p.value[0])}<br/>Margin ${fmtPct(p.value[1])}`,
      },
      xAxis: { type: 'value', name: 'Revenue', nameLocation: 'middle', nameGap: 28, scale: true, ...axisCompact },
      yAxis: { type: 'value', name: 'Margin', scale: true, axisLabel: { formatter: (v: number) => fmtPct(v) } },
      series: ['North', 'South', 'West'].map((r) => ({
        type: 'scatter', name: r, symbolSize: 7, itemStyle: { opacity: 0.75 },
        data: d.customers.filter((c) => c[3] === r),
      })),
    },
    heatmap: {
      grid: { ...grid, top: 8, bottom: 48 },
      tooltip: { position: 'top', formatter: (p: { value: [number, number, number] }) =>
        `${d.byRegion[p.value[1]].name}, ${months[p.value[0]]}<br/>Revenue ${fmtWhole(p.value[2])}` },
      xAxis: { type: 'category', data: months, splitArea: { show: false } },
      yAxis: { type: 'category', data: d.byRegion.map((s) => s.name) },
      visualMap: {
        min: Math.min(...d.byRegion.flatMap((s) => s.data)), max: Math.max(...d.byRegion.flatMap((s) => s.data)),
        orient: 'horizontal', left: 'center', bottom: 0, itemHeight: 160, calculable: false,
        formatter: (v: number) => fmtCompact(v),
        inRange: { color: [token('--heat-lo'), token('--heat-hi')] },
        textStyle: { color: token('--muted') },
      },
      series: [{
        type: 'heatmap', itemStyle: { borderColor: 'transparent', borderWidth: 2, borderRadius: 3 },
        data: d.byRegion.flatMap((s, y) => s.data.map((v, x) => [x, y, v])),
      }],
    },
  }
}
