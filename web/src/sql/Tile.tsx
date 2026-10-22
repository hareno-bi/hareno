import { useMemo } from 'react'
import { Chart } from '../s3/Chart'
import type { Style, Tokens } from '../s3/theme'
import { fmtDelta } from '../s3/format'
import { blank, caption, chartOption, fit, kinds, latest, maps, number, type MatrixSpec, type Result, type Show, type Spec } from './chart'
import { useMap } from './maps'
import MatrixTable from './MatrixTable'
import Table from './Table'

// TileOutput shows one tile's result as its chart, table or matrix. onPick gets the X column and
// the value of a clicked bar, point or slice (not on scatter charts, whose X is a number), or a
// clicked matrix row or column value.
// picks: charts whose X values can be clicked (to filter by them, or drill into them)
export const picks = (k: Spec['kind']) => !['scatter', 'bubble', 'beeswarm', 'gauge', 'histogram', 'waterfall', 'calendar', 'sankey', 'chord', 'sunburst', 'radar'].includes(k)

export default function TileOutput({ result, spec, show, matrix, label, style, vars, themeKey, fontsReady, onPick }: {
  result: Result; spec: Spec | null; show: Show; matrix?: MatrixSpec; label: string; style: Style; vars: Tokens; themeKey: string; fontsReady: boolean
  onPick?: (column: string, value: string) => void
}) {
  const s = fit(spec?.kind ? spec : blank, result)
  const mapReady = useMap(maps(s.kind) ? s.map ?? 'world' : undefined)
  // eslint-disable-next-line react-hooks/exhaustive-deps -- options read colour tokens, which follow themeKey
  const option = useMemo(() => (show === 'chart' && (s.x || s.kind === 'gauge' || s.kind === 'histogram') && s.ys.length && s.kind !== 'kpi' && (mapReady || !maps(s.kind)) ? chartOption(result, s, style, vars) : null), [result, JSON.stringify(s), show, themeKey, mapReady])
  if (show === 'table') return <Table result={result} />
  if (show === 'matrix') return matrix ? <MatrixTable result={result} spec={matrix} onPick={onPick} /> : null
  // no rows (e.g. filtered to nothing): say so, not that columns are missing (with no rows, none look numeric)
  if (show === 'chart' && result.rows.length === 0) return <div className="state empty">No data for the current filters.</div>
  if (show === 'chart' && s.kind === 'kpi' && s.ys.length) return <Kpi result={result} spec={s} />
  if (show === 'chart' && maps(s.kind) && !mapReady) return <div className="state loading">Loading the map…</div>
  if (!option) return <div className="state">Pick a column for X and at least one numeric column for Y.</div>
  return fontsReady ? <Chart theme={themeKey} chart={style.chart} tokens={vars} option={option} label={`${kinds[s.kind]} chart: ${label}`}
    onPick={onPick && picks(s.kind) ? (v) => onPick(s.x, v) : undefined} /> : null
}

// Kpi shows one value large: the latest row's, with its change from the row before (a percent
// value changes by points, anything else by a percentage of the earlier value).
function Kpi({ result, spec }: { result: Result; spec: Spec }) {
  const { value, prior, priorAt } = latest(result, spec)
  const d = result.display, y = spec.ys[0]
  const points = d?.[y]?.format === 'percent'
  const change = value === null || prior === null ? null : points ? value - prior : prior === 0 ? null : (value - prior) / Math.abs(prior)
  const flat = change !== null && Math.abs(change) < 0.0005
  return (
    <div className="kpi tile-kpi" role="figure" aria-label={`${caption(d, y)}: ${value === null ? 'no value' : number(d, y)(value)}`}>
      <div className="kpi-label">{caption(d, y)}</div>
      <div className="kpi-value">{value === null ? '–' : number(d, y)(value)}</div>
      {change !== null && (
        <div className={'kpi-delta ' + (flat ? '' : change > 0 ? 'up' : 'down')}>
          <span aria-hidden>{flat ? '■' : change > 0 ? '▲' : '▼'}</span>{' '}
          {points ? `${flat ? '±' : change > 0 ? '+' : '−'}${Math.abs(change * 100).toFixed(1)} pts` : fmtDelta(change)}
          {' '}<span className="muted">vs {priorAt}</span>
        </div>
      )}
    </div>
  )
}
