import { useEffect, useMemo, useState, type ReactNode } from 'react'
import type { EChartsCoreOption } from 'echarts/core'
import { Chart } from './Chart'
import { fmtCompact, fmtDelta, fmtPct, fmtWhole } from './format'
import { token, type Mode } from './theme'

type Series = { name: string; data: number[] }
type Period = { tonnes: number; flights: number; revenue: number; onTime: number }
type Data = {
  months: string[]
  byFlow: Series[]
  byCargo: Series[]
  monthly: { tonnes: number; onTime: number }[]
  kpi: { current: Period; prior: Period }
  airlines: { name: string; tonnes: number }[]
  shipments: [number, number, string][]
  trucks: [number, number, number][]
  occupancy: number
}

const sum = (a: number[]) => a.reduce((x, y) => x + y, 0)
const monthLabel = (m: string) => new Date(m + '-01T00:00:00').toLocaleDateString('en', { month: 'short', year: '2-digit' })
const tonnes = (v: number) => fmtWhole(v) + ' t'
const axisCompact = { axisLabel: { formatter: (v: number) => fmtCompact(v) } }
const tooltipAxis = { trigger: 'axis', valueFormatter: tonnes }
const days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

export default function Charts({ mode, palette }: { mode: Mode; palette: string }) {
  const [data, setData] = useState<Data | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    fetch(import.meta.env.BASE_URL + 's3/charts.json')
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then(setData, (e: Error) => setError(e.message))
  }, [])
  const theme = mode + palette // charts re-create when either changes
  // eslint-disable-next-line react-hooks/exhaustive-deps -- options read colour tokens, which follow the theme
  const opts = useMemo(() => (data ? options(data) : null), [data, theme])

  if (error) return <p className="note">Sample data missing ({error}). Run <code>web/scripts/s3-snapshot.sh</code>.</p>
  if (!data || !opts) return <div className="grid">{[1, 2, 3, 4].map((i) => <Card key={i} title="" state="loading" />)}</div>
  const { current: cur, prior } = data.kpi
  const chart = (key: string, label: string) => <Chart theme={theme} option={opts[key]} label={label} />
  return (
    <>
      <p className="note">
        Sample set for sign-off, modelled on <strong>Northgate Air Cargo Terminal</strong>, a fictional cargo handling
        facility: twelve invented airlines, about 12,000 flights over 24 months, generated in the dev mart. Amounts
        carry no currency until one is agreed. Switch theme and palette at the top right: the charts are restyled
        from the same settings as the page.
      </p>
      <section className="kpis" aria-label="Key figures, last 12 months">
        <Kpi label="Tonnes handled" value={fmtCompact(cur.tonnes) + ' t'} delta={cur.tonnes / prior.tonnes - 1} />
        <Kpi label="Flights handled" value={fmtWhole(cur.flights)} delta={cur.flights / prior.flights - 1} />
        <Kpi label="Handling revenue" value={fmtCompact(cur.revenue)} delta={cur.revenue / prior.revenue - 1} />
        <Kpi label="ULDs ready by cut-off" value={fmtPct(cur.onTime)} delta={cur.onTime - prior.onTime} points />
      </section>
      <div className="grid">
        <Card title="Tonnage by flow" sub="Monthly, line" wide>
          {chart('line', 'Line chart: monthly tonnage by export, import and transhipment')}
        </Card>
        <Card title="Warehouse occupancy" sub="Now, gauge" stale="Data as of 09:41 · refreshing">
          {chart('gauge', 'Gauge: warehouse occupancy')}
        </Card>
        <Card title="Tonnage by cargo type" sub="Monthly, stacked column" wide>
          {chart('stacked', 'Stacked column chart: monthly tonnage by cargo type')}
        </Card>
        <Card title="Cargo mix" sub="24 months, donut">
          {chart('donut', 'Donut chart: tonnage share by cargo type')}
        </Card>
        <Card title="Tonnage and build-up punctuality" sub="Monthly, combined bar and line, two axes" wide>
          {chart('combo', 'Combined chart: monthly tonnage and share of ULDs ready by cut-off')}
        </Card>
        <Card title="Top 10 airlines" sub="Last 12 months, bar">
          {chart('bar', 'Bar chart: tonnage by airline, top 10')}
        </Card>
        <Card title="Truck arrivals at the landside docks" sub="Average per hour, heatmap" wide>
          {chart('heatmap', 'Heatmap: truck arrivals by weekday and hour')}
        </Card>
        <Card title="Shipment dwell time" sub="400 shipments, weight against hours in terminal, scatter">
          {chart('scatter', 'Scatter chart: shipment weight against dwell time')}
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
  const flat = Math.abs(delta) < 0.0005 // shows as 0.0: no direction
  const shown = points ? `${flat ? '±' : delta > 0 ? '+' : '−'}${Math.abs(delta * 100).toFixed(1)} pts` : fmtDelta(delta)
  return (
    <div className="card kpi">
      <div className="kpi-label">{label}</div>
      <div className="kpi-value">{value}</div>
      <div className={'kpi-delta ' + (flat ? '' : delta > 0 ? 'up' : 'down')}>
        <span aria-hidden>{flat ? '■' : delta > 0 ? '▲' : '▼'}</span> {shown} <span className="muted">vs prior 12 months</span>
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
      series: d.byFlow.map((s) => ({ type: 'line', name: s.name, data: s.data, smooth: 0.2, symbol: 'none', lineStyle: { width: 2 } })),
    },
    gauge: {
      series: [{
        type: 'gauge', startAngle: 210, endAngle: -30, min: 0, max: 1, radius: '92%', center: ['50%', '58%'],
        progress: { show: true, width: 14, roundCap: true },
        axisLine: { lineStyle: { width: 14, color: [[1, token('--grid')]] } },
        axisTick: { show: false }, splitLine: { show: false },
        axisLabel: { show: false },
        pointer: { show: false },
        anchor: { show: false },
        title: { offsetCenter: [0, '28%'], color: token('--muted'), fontSize: 12 },
        detail: { offsetCenter: [0, '-4%'], fontSize: 30, fontWeight: 600, color: token('--text'), formatter: (v: number) => fmtPct(v) },
        data: [{ value: d.occupancy, name: 'of storage positions in use' }],
      }],
    },
    donut: {
      legend: { bottom: 0 },
      tooltip: { trigger: 'item', valueFormatter: tonnes },
      series: [{
        type: 'pie', radius: ['40%', '74%'], center: ['50%', '44%'], padAngle: 1,
        itemStyle: { borderRadius: 4 },
        label: { position: 'inside', color: token('--on-series'), textBorderWidth: 0, fontWeight: 600,
          formatter: (p: { percent: number }) => (p.percent >= 6 ? fmtPct(p.percent / 100) : '') },
        data: d.byCargo.map((s) => ({ name: s.name, value: sum(s.data) })),
      }],
    },
    stacked: {
      grid, legend, tooltip: tooltipAxis,
      xAxis: { type: 'category', data: months },
      yAxis: { type: 'value', ...axisCompact },
      series: d.byCargo.map((s) => ({ type: 'bar', name: s.name, data: s.data, stack: 'total', barMaxWidth: 28 })),
    },
    bar: {
      grid: { ...grid, top: 8 },
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, valueFormatter: tonnes },
      xAxis: { type: 'value', ...axisCompact },
      yAxis: { type: 'category', data: d.airlines.map((a) => a.name), inverse: true },
      series: [{
        type: 'bar', data: d.airlines.map((a) => a.tonnes), barMaxWidth: 18,
        label: { show: true, position: 'right', formatter: (p: { value: number }) => fmtCompact(p.value), color: token('--muted') },
        itemStyle: { borderRadius: [0, 4, 4, 0] },
      }],
    },
    combo: {
      grid: { ...grid, top: 56 }, legend,
      tooltip: { trigger: 'axis' },
      xAxis: { type: 'category', data: months },
      yAxis: [
        { type: 'value', name: 'Tonnes', ...axisCompact },
        { type: 'value', name: 'Ready by cut-off', min: 0.86, max: 0.96, interval: 0.02, splitLine: { show: false }, axisLabel: { formatter: (v: number) => fmtPct(v) } },
      ],
      series: [
        { type: 'bar', name: 'Tonnes', data: d.monthly.map((m) => m.tonnes), barMaxWidth: 24, tooltip: { valueFormatter: tonnes } },
        { type: 'line', name: 'ULDs ready by cut-off', yAxisIndex: 1, data: d.monthly.map((m) => m.onTime), symbolSize: 6, tooltip: { valueFormatter: (v: number) => fmtPct(v) } },
      ],
    },
    scatter: {
      grid: { ...grid, top: 40, bottom: 28, left: 24 }, legend,
      tooltip: {
        trigger: 'item',
        formatter: (p: { value: [number, number, string] }) => `${p.value[2]} shipment<br/>${fmtWhole(p.value[0])} kg, ${p.value[1]} h in terminal`,
      },
      xAxis: { type: 'log', name: 'Weight (kg, log scale)', nameLocation: 'middle', nameGap: 28, ...axisCompact },
      yAxis: { type: 'value', name: 'Hours in terminal', nameLocation: 'middle', nameGap: 32 },
      series: ['Export', 'Import', 'Transhipment'].map((f) => ({
        type: 'scatter', name: f, symbolSize: 7, itemStyle: { opacity: 0.75 },
        data: d.shipments.filter((s) => s[2] === f),
      })),
    },
    heatmap: {
      grid: { ...grid, top: 8, bottom: 48 },
      tooltip: { position: 'top', formatter: (p: { value: [number, number, number] }) =>
        `${days[p.value[1]]} ${String(p.value[0]).padStart(2, '0')}:00<br/>${p.value[2]} trucks per hour` },
      xAxis: { type: 'category', data: Array.from({ length: 24 }, (_, h) => String(h).padStart(2, '0')), splitArea: { show: false } },
      yAxis: { type: 'category', data: days, inverse: true },
      visualMap: {
        min: 0, max: Math.max(...d.trucks.map((t) => t[2])),
        orient: 'horizontal', left: 'center', bottom: 0, itemHeight: 160, calculable: false, text: ['More trucks', 'Fewer'],
        inRange: { color: [token('--heat-lo'), token('--heat-hi')] },
        textStyle: { color: token('--muted') },
      },
      series: [{ type: 'heatmap', itemStyle: { borderColor: 'transparent', borderWidth: 2, borderRadius: 3 }, data: d.trucks }],
    },
  }
}
