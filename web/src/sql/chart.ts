import type { EChartsCoreOption } from 'echarts/core'
import { fmtCompact } from '../s3/format'
import { treatment, type Style, type Tokens } from '../s3/theme'

// A query result and the chart settings built on it (SQL editor and saved reports).
export type Value = string | number | boolean | null
export type Result = { columns: string[]; rows: Value[][]; truncated: boolean }
export const kinds = { bar: 'Column', hbar: 'Bar', line: 'Line', area: 'Area', stacked: 'Stacked column', pie: 'Pie', scatter: 'Scatter' } as const
export type Kind = keyof typeof kinds
export type Spec = { kind: Kind; x: string; ys: string[]; split: string }

export const isNum = (r: Result, i: number) => r.rows.some((row) => row[i] !== null) && r.rows.every((row) => row[i] === null || typeof row[i] === 'number')

// fit keeps the chart choices that still apply to a new result, and picks defaults for the rest:
// X the first text column, Y the first number column after it.
export function fit(s: Spec, r: Result): Spec {
  const has = (c: string) => r.columns.includes(c)
  const nums = r.columns.filter((_, i) => isNum(r, i))
  const x = has(s.x) ? s.x : (r.columns.find((_, i) => !isNum(r, i)) ?? r.columns[0] ?? '')
  const ys = s.ys.filter((y) => nums.includes(y) && y !== x)
  return {
    kind: s.kind,
    x,
    ys: ys.length ? ys : nums.filter((c) => c !== x).slice(0, 1),
    split: has(s.split) && s.split !== x ? s.split : '',
  }
}

const label = (v: Value) => (v === null ? '(null)' : String(v))
const MAX_SERIES = 24

// chartOption turns rows into an ECharts option. With "Series by", rows become one series per
// value of that column; rows with the same X (and series) are added together.
export function chartOption(r: Result, s: Spec, style: Style, vars: Tokens): EChartsCoreOption {
  const { token, line, bar, anim, grid, legend } = treatment(style.chart, vars)
  const col = (c: string) => r.columns.indexOf(c)
  const xi = col(s.x), si = s.split ? col(s.split) : -1
  const fmt = { valueFormatter: (v: number) => (typeof v === 'number' ? v.toLocaleString() : v) }
  const add = (m: Map<string, number>, k: string, v: Value) => m.set(k, (m.get(k) ?? 0) + (typeof v === 'number' ? v : 0))

  if (s.kind === 'pie') {
    const m = new Map<string, number>()
    r.rows.forEach((row) => add(m, label(row[xi]), row[col(s.ys[0])]))
    return {
      ...anim, legend: { bottom: 0, type: 'scroll' }, tooltip: { trigger: 'item', ...fmt },
      series: [{
        type: 'pie', radius: style.chart.donutRadius, center: ['50%', '44%'], padAngle: 1,
        itemStyle: { borderRadius: style.chart.barRadius, borderColor: token('--surface'), borderWidth: style.chart.barRadius ? 0 : 1 },
        data: [...m].map(([name, value]) => ({ name, value })),
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
      const name = si >= 0 ? label(row[si]) : y
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
      xAxis: { type: 'value', name: s.x, nameLocation: 'middle', nameGap: 28, scale: true },
      yAxis: { type: 'value', scale: true, axisLabel: { formatter: fmtCompact } },
      series: names.map((n) => ({
        type: 'scatter', name: n, symbolSize: 7, itemStyle: { opacity: 0.8 },
        data: [...series.get(n)!].map(([x, v]) => [xs.get(x), v]),
      })),
    }
  }

  const cats = [...xs.keys()]
  const horizontal = s.kind === 'hbar'
  const catAxis = { type: 'category', data: cats, boundaryGap: s.kind !== 'line' && s.kind !== 'area', inverse: horizontal }
  const valAxis = { type: 'value', axisLabel: { formatter: fmtCompact } }
  const rad = style.chart.barRadius
  return {
    ...anim, grid, legend: { ...legend, type: 'scroll' }, tooltip: { trigger: 'axis', ...fmt },
    xAxis: horizontal ? valAxis : catAxis,
    yAxis: horizontal ? catAxis : valAxis,
    series: names.map((n, i) => {
      const data = cats.map((c) => series.get(n)!.get(c) ?? null)
      if (s.kind === 'line') return { ...line(i), areaStyle: undefined, name: n, data, connectNulls: true }
      if (s.kind === 'area') return { ...line(i), areaStyle: line(i).areaStyle ?? { opacity: 0.25 }, name: n, data, connectNulls: true }
      const stacked = s.kind === 'stacked'
      return {
        ...bar(i), name: n, data, stack: stacked ? 'total' : undefined,
        itemStyle: { ...bar(i).itemStyle, borderRadius: stacked && i < names.length - 1 ? 0 : horizontal ? [0, rad, rad, 0] : [rad, rad, 0, 0] },
      }
    }),
  }
}

