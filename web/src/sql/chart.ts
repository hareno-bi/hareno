import type { EChartsCoreOption } from 'echarts/core'
import { fmtCompact, fmtPct, fmtWhole, fmtYield } from '../s3/format'
import { treatment, type Style, type Tokens } from '../s3/theme'
import { mapOf, placeOf } from './maps'
import { pack } from '../s3/series'

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
  waterfall: 'Waterfall', histogram: 'Histogram', boxplot: 'Box plot', bullet: 'Bullet',
  sunburst: 'Sunburst', sankey: 'Sankey', chord: 'Chord', radar: 'Radar', calendar: 'Calendar heat map', candlestick: 'Candlestick', beeswarm: 'Beeswarm',
  map: 'Filled map', pointmap: 'Point map', bump: 'Bump', ribbon: 'Ribbon', marimekko: 'Marimekko', waffle: 'Waffle', packed: 'Packed bubbles',
  gantt: 'Gantt', kpi: 'KPI card', gauge: 'Gauge',
} as const
export type Kind = keyof typeof kinds
// max: a gauge's maximum (unset: 100% for a percent, else a round number above the value);
// map: a map's shapes, 'world' (unset) or a country's code for its states or provinces
export type Spec = { kind: Kind; x: string; ys: string[]; split: string; max?: number; map?: string }
export const maps = (k: Kind) => k === 'map' || k === 'pointmap'
// one value: KPI cards and gauges show one; pies, funnels, treemaps, sunbursts, heat maps, calendars
// and waterfalls share one out; flows (Sankey, chord) are sized by one; histograms, box plots and
// beeswarms show how one spreads over the rows
export const oneValue = (k: Kind) => ['pie', 'kpi', 'gauge', 'funnel', 'treemap', 'sunburst', 'heatmap', 'calendar', 'waterfall',
  'sankey', 'chord', 'histogram', 'boxplot', 'beeswarm', 'map', 'bump', 'ribbon', 'marimekko', 'waffle', 'packed'].includes(k)
// several values, in the order picked: a combo's columns and lines, a bubble's height and size, a
// bullet's value and target, a candlestick's open, close, low and high, a point's longitude,
// latitude and size, a Gantt bar's start and end (dates)
const several = (k: Kind) => ({ combo: 2, bubble: 2, bullet: 2, candlestick: 4, pointmap: 3, gantt: 2 } as Partial<Record<Kind, number>>)[k]
// flows go from X to "Series by"
const flows = (k: Kind) => k === 'sankey' || k === 'chord'
// "Series by" splits one value into a series per value of a column (a treemap's groups, a heat
// map's rows); a combo's series are its values, a bubble's values are its height and size
export const splits = (k: Kind) => ['treemap', 'sunburst', 'heatmap', 'sankey', 'chord', 'bump', 'ribbon', 'marimekko', 'packed', 'gantt'].includes(k) || (!oneValue(k) && !several(k))

// a date column: its values are text starting YYYY-MM-DD (a Gantt bar's start and end)
export const isDate = (r: Result, i: number) => r.rows.some((row) => row[i] !== null) && r.rows.every((row) => row[i] === null || /^\d{4}-\d\d-\d\d/.test(String(row[i])))
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
    : s.kind === 'calendar' && !has(s.x) ? (r.columns.find((_, i) => /^\d{4}-\d\d-\d\d/.test(String(r.rows[0]?.[i]))) ?? text ?? '') // a date column
    : has(s.x) ? s.x : (text ?? r.columns[0] ?? '')
  // a Gantt's values are its start and end dates
  const vals = s.kind === 'gantt' ? r.columns.filter((_, i) => isDate(r, i)) : nums
  const ys = s.ys.filter((y) => vals.includes(y) && y !== x)
  const one = oneValue(s.kind), n = several(s.kind)
  // a point map's values default to its longitude and latitude columns, by name
  const geo = (c: string) => (/lon|lng/i.test(c) ? 0 : /lat/i.test(c) ? 1 : 2)
  const pool = s.kind === 'pointmap' ? [...nums].sort((a, b) => geo(a) - geo(b)) : vals
  return {
    kind: s.kind,
    x,
    ys: n && ys.length < n ? [...ys, ...pool.filter((c) => c !== x && !ys.includes(c))].slice(0, n)
      : (ys.length ? ys : pool.filter((c) => c !== x)).slice(0, one ? 1 : n && s.kind !== 'combo' ? n : undefined),
    // a flow needs somewhere to go: the next text column
    split: has(s.split) && s.split !== x && splits(s.kind) ? s.split
      : flows(s.kind) ? (r.columns.find((c, i) => c !== x && !isNum(r, i)) ?? '') : '',
    ...(s.kind === 'gauge' && s.max ? { max: s.max } : {}),
    ...(maps(s.kind) && s.map && s.map !== 'world' ? { map: s.map } : {}),
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
// an axis's numbers: short from 10,000 (12K), in full below, where shortening repeats labels (1.1K, 1.1K)
const fmtAxis = (v: number) => (Math.abs(v) >= 1e4 ? fmtCompact(v) : v.toLocaleString())
const MAX_SERIES = 24

// chartOption turns rows into an ECharts option. With "Series by", rows become one series per
// value of that column; rows with the same X (and series) are added together.
export function chartOption(r: Result, s: Spec, style: Style, vars: Tokens): EChartsCoreOption {
  const { token, colour, line, bar, anim, grid, legend } = treatment(style.chart, vars)
  const col = (c: string) => r.columns.indexOf(c)
  const xi = col(s.x), si = s.split ? col(s.split) : -1
  const d = r.display
  const fmt = (c: string) => ({ valueFormatter: (v: number) => (typeof v === 'number' ? number(d, c)(v) : v) })
  const axisLabel = { formatter: d?.[s.ys[0]]?.format === 'percent' ? fmtPct : fmtAxis }
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
      xAxis: { type: 'value', name: caption(d, s.x), nameLocation: 'middle', nameGap: 28, scale: true, boundaryGap: ['12%', '12%'], axisLabel: { formatter: d?.[s.x]?.format === 'percent' ? fmtPct : fmtAxis } },
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

  if (s.kind === 'candlestick') {
    // a row per X: open, close, low and high, as picked; up days in the rising colour
    const [o, c, lo, hi] = s.ys.map(col), at = (row: Value[], i: number) => (i >= 0 ? row[i] : null)
    return {
      ...anim, grid: { ...grid, top: 16 }, tooltip: { trigger: 'axis', axisPointer: { type: 'cross' } },
      xAxis: { type: 'category', data: r.rows.map((row) => label(row[xi])) },
      yAxis: { type: 'value', scale: true, axisLabel },
      series: [{
        type: 'candlestick', name: caption(d, s.ys[0]),
        itemStyle: { color: token('--up'), color0: token('--down'), borderColor: token('--up'), borderColor0: token('--down') },
        data: r.rows.map((row) => [at(row, o), at(row, c), at(row, lo), at(row, hi)]),
      }],
    }
  }

  if (s.kind === 'beeswarm') {
    // a dot per row, in its X's band, spread sideways so dots do not overlap
    const cats = [...new Set(r.rows.map((row) => label(row[xi])))]
    return {
      ...anim, grid: { ...grid, top: 16 }, tooltip: { trigger: 'item', formatter: (p: { value: [string, number] }) => `${p.value[0]}<br/>${number(d, s.ys[0])(p.value[1])}` },
      xAxis: { type: 'category', data: cats, jitter: 48, jitterOverlap: false, jitterMargin: 2 },
      yAxis: { type: 'value', scale: true, axisLabel },
      series: [{ type: 'scatter', symbolSize: 7, itemStyle: { opacity: 0.8 }, data: r.rows.map((row) => [label(row[xi]), row[col(s.ys[0])]]) }],
    }
  }

  if (s.kind === 'gantt') {
    // a bar per row from its start to its end, a row per X; coloured by "Series by" when set
    const [a, b] = s.ys.map(col), tasks = [...new Set(r.rows.map((row) => label(row[xi])))]
    const groups = si >= 0 ? [...new Set(r.rows.map((row) => label(row[si])))] : ['']
    const time = (v: Value) => (typeof v === 'string' ? Date.parse(v.length === 10 ? v + 'T00:00:00' : v) : NaN) // a date at local midnight
    const ok = (row: Value[]) => !isNaN(time(row[a])) && !isNaN(time(row[b]))
    return {
      ...anim, grid, legend: si >= 0 ? { ...legend, type: 'scroll' } : undefined,
      tooltip: { trigger: 'item', formatter: (p: { name: string; data: { from: string; to: string } }) => `${p.name}<br/>${p.data.from} to ${p.data.to}` },
      xAxis: { type: 'time' }, yAxis: { type: 'category', data: tasks, inverse: true },
      series: groups.map((g) => ({ type: 'custom', renderItem: 'hareno.gantt', name: g || caption(d, s.x), encode: { x: [0, 1], y: 2 },
        itemPayload: { radius: Math.min(style.chart.barRadius, 4) },
        data: r.rows.filter((row) => ok(row) && (si < 0 || label(row[si]) === g))
          .map((row) => ({ name: label(row[xi]), from: label(row[a]), to: label(row[b]), value: [time(row[a]), time(row[b]), tasks.indexOf(label(row[xi]))] })) })),
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

  if (s.kind === 'treemap' || s.kind === 'sunburst') {
    const groups = new Map<string, Map<string, number>>()
    for (const row of r.rows) {
      const g = si >= 0 ? label(row[si]) : ''
      if (!groups.has(g)) groups.set(g, new Map())
      add(groups.get(g)!, label(row[xi]), row[col(s.ys[0])])
    }
    const leaves = (m: Map<string, number>) => [...m].map(([name, value]) => ({ name, value }))
    const data = si >= 0 ? [...groups].map(([name, m]) => ({ name, children: leaves(m) })) : leaves(groups.get('') ?? new Map())
    if (s.kind === 'sunburst') return {
      ...anim, tooltip: { trigger: 'item', ...fmt(s.ys[0]) },
      series: [{
        type: 'sunburst', radius: [si >= 0 ? '12%' : '30%', '92%'], nodeClick: false, sort: undefined,
        itemStyle: { borderColor: token('--surface'), borderWidth: 2, borderRadius: Math.min(style.chart.barRadius, 4) },
        label: { color: token('--text'), minAngle: 12 }, levels: si >= 0 ? [{}, {}, { itemStyle: { opacity: 0.7 } }] : undefined,
        data,
      }],
    }
    return {
      ...anim, tooltip: { trigger: 'item', ...fmt(s.ys[0]) },
      series: [{
        type: 'treemap', top: 0, left: 0, right: 0, bottom: 0, roam: false, nodeClick: false, breadcrumb: { show: false },
        itemStyle: { borderColor: token('--surface'), borderWidth: 1, gapWidth: 1, borderRadius: Math.min(style.chart.barRadius, 4) },
        upperLabel: { show: si >= 0, height: 20, color: token('--text') },
        levels: si >= 0 ? [{ itemStyle: { borderWidth: 0, gapWidth: 3 } }, { colorSaturation: [0.45, 0.75], itemStyle: { gapWidth: 1 } }] : undefined,
        data,
      }],
    }
  }

  if (flows(s.kind)) {
    // flows from X to "Series by", each as wide as its value
    // ponytail: a Sankey cannot draw a loop (A to B and B to A); ECharts reports it instead
    const links = new Map<string, number>(), nodes = new Map<string, true>()
    for (const row of r.rows) {
      const from = label(row[xi]), to = si >= 0 ? label(row[si]) : ''
      if (!to || from === to) continue
      nodes.set(from, true).set(to, true)
      add(links, JSON.stringify([from, to]), row[col(s.ys[0])])
    }
    const data = [...nodes.keys()].map((name) => ({ name }))
    const edges = [...links].filter(([, v]) => v > 0).map(([k, value]) => { const [source, target] = JSON.parse(k); return { source, target, value } })
    const show = number(d, s.ys[0])
    const tooltip = { trigger: 'item', formatter: (p: { dataType: string; name: string; value: number; data: { source?: string; target?: string } }) =>
      p.dataType === 'edge' ? `${p.data.source} → ${p.data.target}<br/>${show(p.value)}` : `${p.name}<br/>${show(p.value)}` }
    if (s.kind === 'chord') return {
      ...anim, tooltip,
      series: [{ type: 'chord', radius: ['74%', '80%'], center: ['50%', '50%'], padAngle: 2, label: { show: true, color: token('--text') },
        lineStyle: { color: 'gradient', opacity: 0.45 }, emphasis: { focus: 'adjacency' }, data, links: edges }],
    }
    return {
      ...anim, tooltip,
      series: [{ type: 'sankey', top: 8, bottom: 8, left: 8, right: 96, nodeGap: 10, draggable: false, emphasis: { focus: 'adjacency' },
        label: { color: token('--text') }, lineStyle: { color: 'gradient', opacity: 0.35 }, itemStyle: { borderWidth: 0 }, data, links: edges }],
    }
  }

  if (s.kind === 'calendar') {
    // a day per cell, coloured by its value; X is a date (or a time, read as its date)
    // ponytail: the date is the text's first ten characters; a raw time's may be in UTC, so pick a day
    // field (labelled in the business time zone) for exact days, or convert here if datasets lack one
    const days = new Map<string, number>()
    r.rows.forEach((row) => add(days, String(row[xi]).slice(0, 10), row[col(s.ys[0])]))
    const keys = [...days.keys()].filter((k) => /^\d{4}-\d\d-\d\d$/.test(k)).sort(), vals = keys.map((k) => days.get(k)!)
    const show = number(d, s.ys[0])
    return {
      ...anim,
      tooltip: { trigger: 'item', formatter: (p: { value: [string, number] }) => `${p.value[0]}<br/>${show(p.value[1])}` },
      visualMap: {
        min: Math.min(0, ...vals), max: Math.max(0, ...vals), orient: 'horizontal', left: 'center', bottom: 0, itemHeight: 160, calculable: false,
        text: [show(Math.max(0, ...vals)), show(Math.min(0, ...vals))], inRange: { color: [token('--heat-lo'), token('--heat-hi')] }, textStyle: { color: token('--muted') },
      },
      calendar: {
        range: keys.length ? [keys[0], keys[keys.length - 1]] : new Date().getFullYear(), top: 28, left: 36, right: 8, bottom: 48, cellSize: ['auto', 'auto'],
        itemStyle: { borderColor: token('--surface'), borderWidth: Math.max(1, style.chart.heatmapGap), color: token('--sub') },
        splitLine: { lineStyle: { color: token('--grid') } }, yearLabel: { show: false },
        dayLabel: { color: token('--muted'), firstDay: 1 }, monthLabel: { color: token('--muted') },
      },
      series: [{ type: 'heatmap', coordinateSystem: 'calendar', data: keys.map((k) => [k, days.get(k)]) }],
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

  if (maps(s.kind)) {
    // a country's or state's shape coloured by its value, or a point per row at its longitude and
    // latitude; places not on the map are listed under it
    const id = s.map ?? 'world', show = number(d, s.ys[0])
    if (!mapOf(id)) return {}
    const small = { fontSize: 10, fontWeight: 'normal', color: token('--muted') }
    const notes = [{ text: 'Boundaries: Natural Earth. They do not imply endorsement.', right: 0, bottom: 0, textStyle: small }]
    const land = { map: id, roam: true, scaleLimit: { min: 1, max: 12 }, itemStyle: { areaColor: token('--sub'), borderColor: token('--surface'), borderWidth: 0.6 } }
    if (s.kind === 'pointmap') {
      const [lon, lat, size] = s.ys.map(col)
      const big = Math.max(...r.rows.map((row) => Math.abs(Number(row[size]) || 0))) || 1
      return {
        ...anim, title: notes, tooltip: { trigger: 'item', formatter: (p: { name: string; value: Value[] }) => (size >= 0 ? `${p.name}<br/>${number(d, s.ys[2])(Number(p.value[2]))}` : p.name) },
        geo: { ...land, silent: true },
        series: [{ type: 'scatter', coordinateSystem: 'geo', itemStyle: { color: colour(0), opacity: 0.75 },
          symbolSize: (v: number[]) => (size >= 0 ? 5 + 25 * Math.sqrt(Math.abs(v[2] ?? 0) / big) : 8),
          data: r.rows.filter((row) => typeof row[lon] === 'number' && typeof row[lat] === 'number').map((row) => ({ name: label(row[xi]), value: [row[lon], row[lat], size >= 0 ? row[size] : 0] })) }],
      }
    }
    // the map's names shown as the dataset's (nameMap), so a click picks the dataset's value
    const nameMap: Record<string, string> = {}, missing: string[] = []
    for (const { name } of shares) {
      const place = placeOf(id, name)
      if (place) nameMap[place] = name
      else missing.push(name)
    }
    const found = shares.filter((x) => !missing.includes(x.name)), vals = found.map((x) => x.value)
    if (missing.length) notes.push({ text: `Not on the map: ${missing.slice(0, 3).join(', ')}${missing.length > 3 ? ` and ${missing.length - 3} more` : ''}`, right: 0, bottom: 14, textStyle: small })
    return {
      ...anim, title: notes,
      tooltip: { trigger: 'item', formatter: (p: { name: string; value: number }) => `${p.name}<br/>${typeof p.value === 'number' && !isNaN(p.value) ? show(p.value) : 'no value'}` },
      visualMap: {
        min: Math.min(0, ...vals), max: Math.max(0, ...vals), orient: 'vertical', left: 0, bottom: 0, itemHeight: 100, calculable: false,
        text: [show(Math.max(0, ...vals)), show(Math.min(0, ...vals))], inRange: { color: [token('--heat-lo'), token('--heat-hi')] }, textStyle: { color: token('--muted') },
      },
      series: [{ type: 'map', ...land, nameMap, select: { disabled: true }, emphasis: { label: { show: false }, itemStyle: { areaColor: token('--active') } }, data: found }],
    }
  }

  if (s.kind === 'waffle') {
    // 100 squares shared out by X; largest remainders, so they add up to 100
    const show = number(d, s.ys[0]), total = shares.reduce((t, x) => t + Math.max(0, x.value), 0) || 1
    const exact = shares.map((x) => (Math.max(0, x.value) / total) * 100), cells = exact.map(Math.floor)
    exact.map((v, i) => [v - cells[i], i]).sort((a, b) => b[0] - a[0]).slice(0, 100 - cells.reduce((a, b) => a + b, 0)).forEach(([, i]) => cells[i]++)
    let at = 0
    return {
      ...anim, legend: { bottom: 0, type: 'scroll' },
      tooltip: { trigger: 'item', formatter: (p: { seriesName: string; seriesIndex: number; data: { raw: number } }) => `${p.seriesName}<br/>${show(p.data.raw)}, ${cells[p.seriesIndex]} of 100 squares` },
      series: shares.map((x, i) => {
        const from = at
        at += cells[i]
        return { type: 'custom', renderItem: 'hareno.waffle', coordinateSystem: 'none', name: x.name, itemPayload: { top: 4, bottom: 28, radius: Math.min(style.chart.barRadius, 3) },
          data: Array.from({ length: cells[i] }, (_, k) => ({ name: x.name, raw: x.value, value: [from + k] })) }
      }),
    }
  }

  if (s.kind === 'packed') {
    // a circle per X, its area its value, packed together; coloured by "Series by" when set
    const show = number(d, s.ys[0]), group = new Map<string, string>()
    if (si >= 0) r.rows.forEach((row) => group.has(label(row[xi])) || group.set(label(row[xi]), label(row[si])))
    const all = shares.filter((x) => x.value > 0).sort((a, b) => b.value - a.value), items = all.slice(0, 100)
    const radii = items.map((x) => Math.sqrt(x.value)), at = pack(radii)
    const extent = [Math.min(...at.map((p, i) => p[0] - radii[i])), Math.min(...at.map((p, i) => p[1] - radii[i])),
      Math.max(...at.map((p, i) => p[0] + radii[i])), Math.max(...at.map((p, i) => p[1] + radii[i]))]
    const groups = si >= 0 ? [...new Set(items.map((x) => group.get(x.name) ?? ''))] : [caption(d, s.ys[0])]
    return {
      ...anim, legend: si >= 0 ? { ...legend, type: 'scroll' } : undefined,
      title: all.length > items.length ? { text: `The largest 100 of ${all.length} shown`, right: 0, bottom: 0, textStyle: { fontSize: 10, fontWeight: 'normal', color: token('--muted') } } : undefined,
      tooltip: { trigger: 'item', formatter: (p: { name: string; data: { raw: number } }) => `${p.name}<br/>${show(p.data.raw)}` },
      series: groups.map((g) => {
        const mine = items.map((x, i) => [x, i] as const).filter(([x]) => si < 0 || group.get(x.name) === g)
        return { type: 'custom', renderItem: 'hareno.packed', coordinateSystem: 'none', name: g,
          itemPayload: { extent, top: si >= 0 ? 28 : 4, color: token('--on-series'), names: mine.map(([x]) => x.name) },
          data: mine.map(([x, i]) => ({ name: x.name, raw: x.value, value: [at[i][0], at[i][1], radii[i]] })) }
      }),
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

  if (s.kind === 'bump') {
    // each X's series ranked by value, 1 the largest; a line per series through its ranks
    const cats = [...xs.keys()], show = number(d, s.ys[0]), rank = new Map<string, Map<string, number>>()
    for (const c of cats) names.filter((n) => series.get(n)!.has(c)).sort((a, b) => series.get(b)!.get(c)! - series.get(a)!.get(c)!)
      .forEach((n, i) => (rank.get(n) ?? rank.set(n, new Map()).get(n)!).set(c, i + 1))
    return {
      ...anim, grid: { ...grid, right: 96 }, legend: { ...legend, type: 'scroll' },
      tooltip: { trigger: 'item', formatter: (p: { seriesName: string; name: string; data: { value: number; raw: number } }) => `${p.seriesName}, ${p.name}<br/>No. ${p.data.value}: ${show(p.data.raw)}` },
      xAxis: { type: 'category', data: cats, boundaryGap: false },
      yAxis: { type: 'value', inverse: true, min: 1, max: Math.max(names.length, 1), interval: 1, axisLabel: { formatter: (v: number) => `No. ${v}` } },
      series: names.map((n, i) => ({ ...line(i), areaStyle: undefined, symbol: 'circle', symbolSize: 8, showSymbol: true, name: n,
        endLabel: { show: true, formatter: '{a}', color: token('--text') },
        data: cats.map((c) => (rank.get(n)?.has(c) ? { value: rank.get(n)!.get(c), raw: series.get(n)!.get(c) } : null)) })),
    }
  }

  if (s.kind === 'ribbon') {
    // each X a column of its series stacked by size, largest on top; ribbons join a series' segments
    const cats = [...xs.keys()], show = number(d, s.ys[0])
    const pos = cats.map((c) => {
      const m = new Map<string, [number, number]>()
      let y = 0
      for (const n of names.filter((n) => (series.get(n)!.get(c) ?? 0) > 0).sort((a, b) => series.get(a)!.get(c)! - series.get(b)!.get(c)!)) {
        const v = series.get(n)!.get(c)!
        m.set(n, [y, y + v])
        y += v
      }
      return m
    })
    return {
      ...anim, grid, legend: { ...legend, type: 'scroll' },
      tooltip: { trigger: 'item', formatter: (p: { seriesName: string; name: string; data: { raw: number } }) => `${p.seriesName}, ${p.name}<br/>${show(p.data.raw)}` },
      xAxis: { type: 'category', data: cats }, yAxis: { type: 'value', axisLabel },
      series: names.map((n) => ({ type: 'custom', renderItem: 'hareno.ribbon', name: n, encode: { x: 0, y: [1, 2] },
        data: cats.flatMap((c, j) => {
          const here = pos[j].get(n), next = pos[j + 1]?.get(n)
          return here ? [{ name: c, raw: series.get(n)!.get(c), value: [j, here[0], here[1], next?.[0] ?? null, next?.[1] ?? null] }] : []
        }) })),
    }
  }

  if (s.kind === 'marimekko') {
    // each X a column as wide as its share of the total; its series stacked, each as tall as its share of the column
    const cats = [...xs.keys()], show = number(d, s.ys[0])
    const colTotal = cats.map((c) => names.reduce((t, n) => t + Math.max(0, series.get(n)!.get(c) ?? 0), 0)), total = colTotal.reduce((a, b) => a + b, 0) || 1
    let x = 0
    const cols = colTotal.map((t) => [x, (x += t / total)])
    const filled = cats.map(() => 0)
    return {
      ...anim, grid: { ...grid, bottom: 24 }, legend: { ...legend, type: 'scroll' },
      tooltip: { trigger: 'item', formatter: (p: { seriesName: string; name: string; data: { raw: number; share: number } }) =>
        `${p.seriesName}, ${p.name}<br/>${show(p.data.raw)}, ${fmtPct(p.data.share)} of ${p.name}` },
      xAxis: { type: 'value', min: 0, max: 1, axisLabel: { show: false }, axisTick: { show: false }, splitLine: { show: false } },
      yAxis: { type: 'value', min: 0, max: 1, axisLabel: { formatter: fmtPct } },
      series: names.map((n) => ({ type: 'custom', renderItem: 'hareno.marimekko', name: n, encode: { x: [0, 1], y: [2, 3] }, itemPayload: { columns: cats, color: token('--muted') },
        data: cats.flatMap((c, j) => {
          const v = Math.max(0, series.get(n)!.get(c) ?? 0)
          if (!v || !colTotal[j]) return []
          const y0 = filled[j], h = v / colTotal[j]
          filled[j] += h
          return [{ name: c, raw: v, share: h, value: [cols[j][0], cols[j][1], y0, y0 + h, j] }]
        }) })),
    }
  }

  if (s.kind === 'radar') {
    // X values are the spokes, one shape per value (or per "Series by" value), all on one scale
    const spokes = [...xs.keys()], peak = Math.max(0, ...names.flatMap((n) => [...series.get(n)!.values()]))
    return {
      ...anim, legend: { ...legend, type: 'scroll' }, tooltip: { trigger: 'item' },
      radar: { indicator: spokes.map((name) => ({ name, max: roundUp(peak) })), radius: '64%', center: ['50%', '56%'],
        axisName: { color: token('--muted') }, splitLine: { lineStyle: { color: token('--grid') } }, splitArea: { show: false }, axisLine: { lineStyle: { color: token('--grid') } } },
      series: [{
        type: 'radar', symbolSize: 4,
        data: names.map((n, i) => ({ name: n, value: spokes.map((c) => series.get(n)!.get(c) ?? 0), areaStyle: { opacity: 0.15 }, lineStyle: { width: style.chart.lineWidth, color: colour(i) }, itemStyle: { color: colour(i) } })),
      }],
    }
  }

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
  const comboAxes = [valAxis, { type: 'value', splitLine: { show: false }, axisLabel: { formatter: d?.[s.ys[1]]?.format === 'percent' ? fmtPct : fmtAxis } }]
  const rad = style.chart.barRadius
  return {
    ...anim, grid, legend: { ...legend, type: 'scroll' }, tooltip: { trigger: 'axis', ...fmt(s.ys[0]) },
    xAxis: horizontal ? valAxis : catAxis,
    yAxis: horizontal ? catAxis : combo ? comboAxes : valAxis,
    series: names.map((n, i) => {
      const raw = cats.map((c) => series.get(n)!.get(c) ?? (stack ? 0 : null)) // a stack counts a missing value as 0, or its layers break
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
