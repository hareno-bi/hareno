import type { EChartsCoreOption } from 'echarts/core'
import { fmtCompact, fmtPct, fmtWhole, fmtYield } from '../s3/format'
import { treatment, type Style, type Tokens } from '../s3/theme'

// A query result and the chart settings built on it (SQL editor and saved reports).
export type Value = string | number | boolean | null
export type Result = { columns: string[]; rows: Value[][]; truncated: boolean; display?: Labels }
// How a dataset's columns are shown, by column (server: model.Display). SQL tiles have none.
export type Format = 'number' | 'integer' | 'money' | 'percent'
export type Labels = Record<string, { label: string; format?: Format }>
export const caption = (d: Labels | undefined, c: string) => d?.[c]?.label ?? c
const formats: Record<Format, (v: number) => string> = {
  number: (v) => v.toLocaleString(undefined, { maximumFractionDigits: 2 }), integer: fmtWhole, money: fmtYield, percent: fmtPct,
}
// number shows v as column c's format says, or as before (unformatted) where there is none.
export const number = (d: Labels | undefined, c: string) => (v: number) => {
  const f = d?.[c] && formats[d[c].format ?? 'number']
  return f ? f(v) : v.toLocaleString()
}
export const kinds = {
  bar: 'Column', hbar: 'Bar', stacked: 'Stacked column', hstacked: 'Stacked bar', pct: '100% stacked column', hpct: '100% stacked bar',
  line: 'Line', area: 'Area', stackedArea: 'Stacked area', combo: 'Column and line', pie: 'Pie', funnel: 'Funnel', treemap: 'Treemap', heatmap: 'Heat map', scatter: 'Scatter', bubble: 'Bubble',
  waterfall: 'Waterfall', histogram: 'Histogram', boxplot: 'Box plot', bullet: 'Bullet', kpi: 'KPI card', gauge: 'Gauge',
} as const
export type Kind = keyof typeof kinds
// max: a gauge's maximum (unset: 100% for a percent, else a round number above the value)
export type Spec = { kind: Kind; x: string; ys: string[]; split: string; max?: number }
// one value: KPI cards and gauges show one; pies, funnels, treemaps, heat maps and waterfalls share
// one out; histograms and box plots show how one spreads over the rows
export const oneValue = (k: Kind) => ['pie', 'kpi', 'gauge', 'funnel', 'treemap', 'heatmap', 'waterfall', 'histogram', 'boxplot'].includes(k)
// two values: a combo's columns and line, a bubble's height and size, a bullet's value and target
const twoValues = (k: Kind) => k === 'combo' || k === 'bubble' || k === 'bullet'
// "Series by" splits one value into a series per value of a column (a treemap's groups, a heat
// map's rows); a combo's series are its values, a bubble's values are its height and size
export const splits = (k: Kind) => k === 'treemap' || k === 'heatmap' || (!oneValue(k) && !twoValues(k))

export const isNum = (r: Result, i: number) => r.rows.some((row) => row[i] !== null) && r.rows.every((row) => row[i] === null || typeof row[i] === 'number')

// fit keeps the chart choices that still apply to a new result, and picks defaults for the rest:
// X the first text column, Y the first number column after it.
export function fit(s: Spec, r: Result): Spec {
  const has = (c: string) => r.columns.includes(c)
  // number columns, those with a number format (a dataset's measures, not e.g. its order numbers) first
  const nums = r.columns.filter((_, i) => isNum(r, i)).sort((a, b) => Number(!r.display?.[a]?.format) - Number(!r.display?.[b]?.format))
  const text = r.columns.find((_, i) => !isNum(r, i))
  // a KPI card or gauge needs no X; its X is a text column, never the number it shows
  const latestBy = s.kind === 'kpi' || s.kind === 'gauge'
  // a bubble's X is a number: bubbles sit across by one value, up by another, sized by a third
  const x = latestBy ? (has(s.x) && !nums.includes(s.x) ? s.x : text ?? '')
    : s.kind === 'bubble' ? (nums.includes(s.x) ? s.x : nums[0] ?? '')
    : has(s.x) ? s.x : (text ?? r.columns[0] ?? '')
  const ys = s.ys.filter((y) => nums.includes(y) && y !== x)
  const one = oneValue(s.kind)
  return {
    kind: s.kind,
    x,
    ys: twoValues(s.kind) && ys.length < 2 ? [...ys, ...nums.filter((c) => c !== x && !ys.includes(c))].slice(0, 2)
      : (ys.length ? ys : nums.filter((c) => c !== x)).slice(0, one ? 1 : twoValues(s.kind) && s.kind !== 'combo' ? 2 : undefined),
    split: has(s.split) && s.split !== x && splits(s.kind) ? s.split : '',
    ...(s.kind === 'gauge' && s.max ? { max: s.max } : {}),
  }
}

// latest is what a KPI card or gauge shows: the value in the last row (rows come ordered by X,
// e.g. by month), and the row before it to compare with.
export function latest(r: Result, s: Spec) {
  const xi = r.columns.indexOf(s.x), yi = r.columns.indexOf(s.ys[0])
  const at = (i: number) => (i >= 0 && typeof r.rows[i]?.[yi] === 'number' ? r.rows[i][yi] as number : null)
  const n = r.rows.length
  return { value: at(n - 1), prior: n > 1 ? at(n - 2) : null, priorAt: n < 2 ? '' : xi >= 0 ? label(r.rows[n - 2][xi]) : 'the row before' }
}

// roundUp is a gauge's default maximum: 734.75 -> 800, 0.42 -> 0.5
export function roundUp(v: number) {
  if (v <= 0) return 1
  const step = 10 ** Math.floor(Math.log10(v))
  return Math.ceil(v / step) * step
}

const label = (v: Value) => (v === null ? '(null)' : String(v))
const MAX_SERIES = 24

// chartOption turns rows into an ECharts option. With "Series by", rows become one series per
// value of that column; rows with the same X (and series) are added together.
export function chartOption(r: Result, s: Spec, style: Style, vars: Tokens): EChartsCoreOption {
  const { token, colour, line, bar, anim, grid, legend } = treatment(style.chart, vars)
  const col = (c: string) => r.columns.indexOf(c)
  const xi = col(s.x), si = s.split ? col(s.split) : -1
  const d = r.display
  const fmt = (c: string) => ({ valueFormatter: (v: number) => (typeof v === 'number' ? number(d, c)(v) : v) })
  const axisLabel = { formatter: d?.[s.ys[0]]?.format === 'percent' ? fmtPct : fmtCompact }
  const add = (m: Map<string, number>, k: string, v: Value) => m.set(k, (m.get(k) ?? 0) + (typeof v === 'number' ? v : 0))

  if (s.kind === 'gauge') {
    const { value } = latest(r, s)
    const show = number(d, s.ys[0])
    const max = s.max ?? (d?.[s.ys[0]]?.format === 'percent' ? 1 : roundUp(value ?? 0))
    return {
      ...anim,
      series: [{
        type: 'gauge', startAngle: 210, endAngle: -30, min: 0, max, radius: '92%', center: ['50%', '58%'],
        progress: { show: true, width: style.chart.gaugeWidth, roundCap: style.chart.barRadius > 0 },
        axisLine: { roundCap: style.chart.barRadius > 0, lineStyle: { width: style.chart.gaugeWidth, color: [[1, token('--grid')]] } },
        axisTick: { show: false }, splitLine: { show: false }, pointer: { show: false }, anchor: { show: false },
        axisLabel: { show: false },
        title: { offsetCenter: [0, '28%'], color: token('--muted'), fontSize: 12 },
        detail: { offsetCenter: [0, '-4%'], fontSize: 30, fontWeight: Number(token('--kpi-weight')), fontFamily: token('--font-display'), color: token('--text'),
          formatter: (v: number) => (value === null ? '–' : show(v)) },
        data: [{ value: value ?? 0, name: `${caption(d, s.ys[0])} of ${show(max)}` }],
      }],
    }
  }

  if (s.kind === 'bubble') {
    // one bubble per row, named by its first text column; size by area, the largest 48px across
    const [up, size] = s.ys.map(col), ni = r.columns.findIndex((_, i) => !isNum(r, i))
    const big = Math.max(...r.rows.map((row) => Math.abs(Number(row[size]) || 0))) || 1
    const show = (c: string, v: Value) => `${caption(d, c)}: ${typeof v === 'number' ? number(d, c)(v) : label(v)}`
    return {
      ...anim, grid: { ...grid, top: 16, bottom: 28, left: 40 },
      tooltip: { trigger: 'item', formatter: (p: { name: string; data: { row: Value[] } }) =>
        [p.name, show(s.x, p.data.row[xi]), show(s.ys[0], p.data.row[up]), ...(size >= 0 ? [show(s.ys[1], p.data.row[size])] : [])].filter(Boolean).join('<br/>') },
      // room around the outer bubbles
      xAxis: { type: 'value', name: caption(d, s.x), nameLocation: 'middle', nameGap: 28, scale: true, boundaryGap: ['12%', '12%'], axisLabel: { formatter: d?.[s.x]?.format === 'percent' ? fmtPct : fmtCompact } },
      yAxis: { type: 'value', name: caption(d, s.ys[0]), nameLocation: 'middle', nameGap: 40, scale: true, boundaryGap: ['15%', '15%'], axisLabel },
      series: [{
        type: 'scatter', itemStyle: { opacity: 0.7 }, label: { show: true, position: 'top', formatter: '{b}', color: token('--muted') },
        symbolSize: (v: number[]) => (size >= 0 ? 6 + 42 * Math.sqrt(Math.abs(v[2] ?? 0) / big) : 12),
        data: r.rows.map((row) => ({ name: ni >= 0 ? label(row[ni]) : '', row, value: [row[xi], row[up], size >= 0 ? row[size] : 0] })),
      }],
    }
  }

  if (s.kind === 'histogram') {
    // how the value spreads over the rows: Sturges' number of bins, each the same width
    const vs = r.rows.map((row) => row[col(s.ys[0])]).filter((v): v is number => typeof v === 'number')
    const lo = vs.reduce((a, v) => Math.min(a, v), vs[0] ?? 0), hi = vs.reduce((a, v) => Math.max(a, v), vs[0] ?? 0)
    const n = Math.ceil(Math.log2(vs.length || 1)) + 1, w = (hi - lo) / n || 1
    const counts: number[] = Array(n).fill(0)
    vs.forEach((v) => counts[Math.min(n - 1, Math.floor((v - lo) / w))]++)
    const show = number(d, s.ys[0])
    return {
      ...anim, grid: { ...grid, top: 16 }, tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
      xAxis: { type: 'category', data: counts.map((_, i) => `${show(lo + i * w)} – ${show(lo + (i + 1) * w)}`), name: caption(d, s.ys[0]), nameLocation: 'middle', nameGap: 28 },
      yAxis: { type: 'value', minInterval: 1 },
      series: [{ ...bar(0), name: 'Rows', barCategoryGap: '4%', barMaxWidth: undefined, data: counts }],
    }
  }

  if (s.kind === 'boxplot') {
    // each X's spread of the value over its rows: lowest, quartiles, median, highest
    const groups = new Map<string, number[]>()
    for (const row of r.rows) {
      const v = row[col(s.ys[0])], x = label(row[xi])
      if (typeof v !== 'number') continue
      if (!groups.has(x)) groups.set(x, [])
      groups.get(x)!.push(v)
    }
    const q = (a: number[], p: number) => { const i = (a.length - 1) * p, f = Math.floor(i); return a[f] + (a[Math.ceil(i)] - a[f]) * (i - f) }
    const boxes = [...groups].map(([name, a]) => (a.sort((u, v) => u - v), { name, value: [0, 0.25, 0.5, 0.75, 1].map((p) => q(a, p)), n: a.length }))
    const show = number(d, s.ys[0])
    return {
      ...anim, grid: { ...grid, top: 16 },
      tooltip: { trigger: 'item', formatter: (p: { name: string; data: { value: number[]; n: number } }) =>
        [p.name, ...['Highest', 'Upper quartile', 'Median', 'Lower quartile', 'Lowest'].map((t, i) => `${t}: ${show(p.data.value[4 - i])}`), `${p.data.n} rows`].join('<br/>') },
      xAxis: { type: 'category', data: boxes.map((b) => b.name) },
      yAxis: { type: 'value', scale: true, axisLabel },
      series: [{ type: 'boxplot', itemStyle: { color: colour(0) + '33', borderColor: colour(0) }, data: boxes }],
    }
  }

  if (s.kind === 'bullet') {
    // each X's value as a bar, its target as a mark across it
    const vm = new Map<string, number>(), tm = new Map<string, number>()
    r.rows.forEach((row) => { add(vm, label(row[xi]), row[col(s.ys[0])]); if (s.ys[1]) add(tm, label(row[xi]), row[col(s.ys[1])]) })
    const cats = [...vm.keys()], rad = style.chart.barRadius
    return {
      ...anim, grid, legend, tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
      xAxis: { type: 'value', axisLabel }, yAxis: { type: 'category', data: cats, inverse: true },
      series: [
        { ...bar(0), name: caption(d, s.ys[0]), barMaxWidth: 16, tooltip: fmt(s.ys[0]), itemStyle: { ...bar(0).itemStyle, borderRadius: [0, rad, rad, 0] },
          data: cats.map((c) => vm.get(c)) },
        ...(s.ys[1] ? [{ type: 'scatter', name: caption(d, s.ys[1]), symbol: 'rect', symbolSize: [3, 26], z: 3, itemStyle: { color: token('--text') },
          tooltip: fmt(s.ys[1]), data: cats.map((c, i) => [tm.get(c) ?? null, i]) }] : []),
      ],
    }
  }

  // one value shared out by X: slices, funnel steps, treemap boxes (grouped by "Series by")
  const m = new Map<string, number>()
  r.rows.forEach((row) => add(m, label(row[xi]), row[col(s.ys[0])]))
  const shares = [...m].map(([name, value]) => ({ name, value }))

  if (s.kind === 'waterfall') {
    // each X a step up or down from the running total, then the total
    // ponytail: assumes the running total stays at or above zero; stacked bars below it would need a different base
    let run = 0
    const steps = shares.map(({ name, value }) => { const from = run; run += value; return { name, value, from } })
    const show = number(d, s.ys[0])
    return {
      ...anim, grid: { ...grid, top: 16 },
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, formatter: (ps: { dataIndex: number }[]) => {
        const st = steps[ps[0].dataIndex]
        return st ? `${st.name}<br/>${show(st.value)}, running total ${show(st.from + st.value)}` : `Total<br/>${show(run)}`
      } },
      xAxis: { type: 'category', data: [...steps.map((x) => x.name), 'Total'] },
      yAxis: { type: 'value', axisLabel },
      series: [
        { type: 'bar', stack: 'w', silent: true, itemStyle: { color: 'transparent' }, data: [...steps.map((x) => Math.min(x.from, x.from + x.value)), 0] },
        { ...bar(0), stack: 'w', data: [...steps.map((x) => ({ value: Math.abs(x.value), itemStyle: { color: token(x.value < 0 ? '--down' : '--up') } })),
          { value: run, itemStyle: { color: colour(0) } }] },
      ],
    }
  }

  if (s.kind === 'funnel') {
    return {
      ...anim, tooltip: { trigger: 'item', ...fmt(s.ys[0]) },
      series: [{
        type: 'funnel', top: 8, bottom: 8, left: '4%', width: '62%', sort: 'descending', gap: 2, minSize: '8%',
        label: { position: 'right', color: token('--text'), formatter: (p: { name: string; value: number }) => `${p.name}  ${number(d, s.ys[0])(p.value)}` }, itemStyle: { borderWidth: 0, borderRadius: style.chart.barRadius },
        data: shares,
      }],
    }
  }

  if (s.kind === 'treemap') {
    const groups = new Map<string, Map<string, number>>()
    for (const row of r.rows) {
      const g = si >= 0 ? label(row[si]) : ''
      if (!groups.has(g)) groups.set(g, new Map())
      add(groups.get(g)!, label(row[xi]), row[col(s.ys[0])])
    }
    const leaves = (m: Map<string, number>) => [...m].map(([name, value]) => ({ name, value }))
    return {
      ...anim, tooltip: { trigger: 'item', ...fmt(s.ys[0]) },
      series: [{
        type: 'treemap', top: 0, left: 0, right: 0, bottom: 0, roam: false, nodeClick: false, breadcrumb: { show: false },
        itemStyle: { borderColor: token('--surface'), borderWidth: 1, gapWidth: 1, borderRadius: Math.min(style.chart.barRadius, 4) },
        upperLabel: { show: si >= 0, height: 20, color: token('--text') },
        levels: si >= 0 ? [{ itemStyle: { borderWidth: 0, gapWidth: 3 } }, { colorSaturation: [0.45, 0.75], itemStyle: { gapWidth: 1 } }] : undefined,
        data: si >= 0 ? [...groups].map(([name, m]) => ({ name, children: leaves(m) })) : leaves(groups.get('') ?? new Map()),
      }],
    }
  }

  if (s.kind === 'heatmap') {
    // X across, "Series by" down, the value as colour; without "Series by", one row
    const cells = new Map<string, number>(), across = new Map<string, true>(), down = new Map<string, true>()
    for (const row of r.rows) {
      const x = label(row[xi]), y = si >= 0 ? label(row[si]) : caption(d, s.ys[0])
      across.set(x, true)
      down.set(y, true)
      add(cells, JSON.stringify([x, y]), row[col(s.ys[0])])
    }
    const xs = [...across.keys()], ys = [...down.keys()], vals = [...cells.values()]
    const show = number(d, s.ys[0])
    return {
      ...anim, grid: { ...grid, top: 8, bottom: 48 },
      tooltip: { trigger: 'item', formatter: (p: { value: [number, number, number] }) => `${xs[p.value[0]]}, ${ys[p.value[1]]}<br/>${show(p.value[2])}` },
      xAxis: { type: 'category', data: xs, splitArea: { show: false } },
      yAxis: { type: 'category', data: ys, inverse: true },
      visualMap: {
        min: Math.min(0, ...vals), max: Math.max(...vals), orient: 'horizontal', left: 'center', bottom: 0, itemHeight: 160, calculable: false,
        text: [show(Math.max(...vals)), show(Math.min(0, ...vals))], inRange: { color: [token('--heat-lo'), token('--heat-hi')] }, textStyle: { color: token('--muted') },
      },
      series: [{
        type: 'heatmap', label: { show: true, formatter: (p: { value: number[] }) => show(p.value[2]) }, itemStyle: { borderColor: token('--surface'), borderWidth: style.chart.heatmapGap, borderRadius: Math.min(style.chart.barRadius, 4) },
        data: [...cells].map(([k, v]) => { const [x, y] = JSON.parse(k); return { name: x, value: [xs.indexOf(x), ys.indexOf(y), v] } }),
      }],
    }
  }

  if (s.kind === 'pie') {
    return {
      ...anim, legend: { bottom: 0, type: 'scroll' }, tooltip: { trigger: 'item', ...fmt(s.ys[0]) },
      series: [{
        type: 'pie', radius: style.chart.donutRadius, center: ['50%', '44%'], padAngle: 1,
        itemStyle: { borderRadius: style.chart.barRadius, borderColor: token('--surface'), borderWidth: style.chart.barRadius ? 0 : 1 },
        data: shares,
      }],
    }
  }

  // series: name -> values keyed by X
  const series = new Map<string, Map<string, number>>()
  const xs = new Map<string, Value>()
  for (const row of r.rows) {
    const x = label(row[xi])
    xs.set(x, row[xi])
    for (const y of s.ys) {
      const name = si >= 0 ? label(row[si]) : caption(d, y)
      if (!series.has(name)) {
        if (series.size >= MAX_SERIES) continue
        series.set(name, new Map())
      }
      add(series.get(name)!, x, row[col(y)])
    }
  }
  const names = [...series.keys()]

  if (s.kind === 'scatter') {
    return {
      ...anim, grid: { ...grid, bottom: 28, left: 24 }, legend, tooltip: { trigger: 'item' },
      xAxis: { type: 'value', name: caption(d, s.x), nameLocation: 'middle', nameGap: 28, scale: true },
      yAxis: { type: 'value', scale: true, axisLabel },
      series: names.map((n) => ({
        type: 'scatter', name: n, symbolSize: 7, itemStyle: { opacity: 0.8 },
        data: [...series.get(n)!].map(([x, v]) => [xs.get(x), v]),
      })),
    }
  }

  const cats = [...xs.keys()]
  const k = s.kind
  const horizontal = k === 'hbar' || k === 'hstacked' || k === 'hpct'
  const pct = k === 'pct' || k === 'hpct' // each category's values as shares of its total
  const stack = pct || k === 'stacked' || k === 'hstacked' || k === 'stackedArea' ? 'total' : undefined
  const lines = k === 'line' || k === 'area' || k === 'stackedArea'
  const totals = cats.map((c) => names.reduce((t, n) => t + Math.abs(series.get(n)!.get(c) ?? 0), 0))
  const catAxis = { type: 'category', data: cats, boundaryGap: !lines, inverse: horizontal }
  const valAxis = pct ? { type: 'value', max: 1, axisLabel: { formatter: fmtPct } } : { type: 'value', axisLabel }
  // a combo's first value is columns on the left axis, the others lines on the right, each in its format
  const combo = k === 'combo' && names.length > 1
  const comboAxes = [valAxis, { type: 'value', splitLine: { show: false }, axisLabel: { formatter: d?.[s.ys[1]]?.format === 'percent' ? fmtPct : fmtCompact } }]
  const rad = style.chart.barRadius
  return {
    ...anim, grid, legend: { ...legend, type: 'scroll' }, tooltip: { trigger: 'axis', ...fmt(s.ys[0]) },
    xAxis: horizontal ? valAxis : catAxis,
    yAxis: horizontal ? catAxis : combo ? comboAxes : valAxis,
    series: names.map((n, i) => {
      const raw = cats.map((c) => series.get(n)!.get(c) ?? null)
      const data = pct ? raw.map((v, j) => (v === null || !totals[j] ? null : v / totals[j])) : raw
      const tooltip = pct ? { valueFormatter: (v: number) => (typeof v === 'number' ? fmtPct(v) : v) } : fmt(si >= 0 ? s.ys[0] : s.ys[i]) // each Y in its own format
      if (k === 'line' || (combo && i > 0)) return { ...line(i), areaStyle: undefined, name: n, data, tooltip, connectNulls: true, yAxisIndex: combo ? 1 : 0 }
      if (lines) return { ...line(i), areaStyle: stack ? { opacity: 0.85 } : line(i).areaStyle ?? { opacity: 0.25 }, name: n, data, tooltip, connectNulls: true, stack }
      return {
        ...bar(i), name: n, data, tooltip, stack,
        itemStyle: { ...bar(i).itemStyle, borderRadius: stack && i < names.length - 1 ? 0 : horizontal ? [0, rad, rad, 0] : [rad, rad, 0, 0] },
      }
    }),
  }
}


// A dashboard (a saved report): tiles, each a query and how it is shown, half or full width.
export type Show = 'chart' | 'table' | 'matrix'
export type TileDef = { title: string; sql: string; chart: Spec | null; show: Show; wide: boolean; matrix?: MatrixSpec; dataset?: Pick }

// A dataset tile's picks: the dataset, the fields to group by and the measures (server: datasets.go).
// Saved instead of SQL; the server writes the query each time the tile runs.
export type Pick = { dataset: string; dimensions: string[]; measures: string[] }
export type Dataset = { name: string; connection: string; dimensions: string[]; measures: string[]; display: Labels }

// A matrix tile: rows grouped by up to three columns and across by one, each value aggregated.
// The server totals them (internal/sqlapi/matrix.go).
export const aggs = { sum: 'Sum', avg: 'Average', min: 'Min', max: 'Max', count: 'Count' } as const
export type Agg = keyof typeof aggs
// On a dataset tile the values are measures, which the dataset totals itself: agg is ''.
export type MatrixSpec = { rows: string[]; cols: string[]; values: { column: string; agg: Agg | '' }[] }

// fitMatrix keeps the matrix choices that still apply to a new result: rows default to the first
// text column, the value to the sum of the first number column. On a dataset tile (measures given),
// rows and columns are its other columns and values its measures.
export function fitMatrix(m: MatrixSpec | undefined, r: Result, measures?: string[]): MatrixSpec {
  const has = (c: string) => r.columns.includes(c) && !measures?.includes(c)
  const isValue = (c: string) => r.columns.includes(c) && (!measures || measures.includes(c))
  const rows = (m?.rows ?? []).filter(has)
  const cols = (m?.cols ?? []).filter((c) => has(c) && !rows.includes(c))
  const values = (m?.values ?? []).filter((v) => isValue(v.column) && (v.agg === '') === !!measures)
  const text = r.columns.find((c, i) => has(c) && !isNum(r, i)) ?? r.columns.find(has)
  const num = measures ? measures.find(isValue) : r.columns.find((c, i) => isNum(r, i) && c !== text)
  return {
    rows: rows.length || !text ? rows : [text],
    cols,
    values: values.length || !num ? values : [{ column: num, agg: measures ? '' : 'sum' }],
  }
}
export type Dash = { title: string; tiles: TileDef[]; filters?: string[] } // filters: the columns the filter bar offers
export const blank: Spec = { kind: 'bar', x: '', ys: [], split: '' }
