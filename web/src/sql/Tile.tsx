import { useMemo } from 'react'
import { Chart } from '../s3/Chart'
import type { Style, Tokens } from '../s3/theme'
import { blank, chartOption, fit, kinds, type Result, type Show, type Spec } from './chart'
import Table from './Table'

// TileOutput shows one tile's result as its chart or its table. onPick gets the X column and the
// value of a clicked bar, point or slice (not on scatter charts, whose X is a number).
export default function TileOutput({ result, spec, show, label, style, vars, themeKey, fontsReady, onPick }: {
  result: Result; spec: Spec | null; show: Show; label: string; style: Style; vars: Tokens; themeKey: string; fontsReady: boolean
  onPick?: (column: string, value: string) => void
}) {
  const s = fit(spec?.kind ? spec : blank, result)
  // eslint-disable-next-line react-hooks/exhaustive-deps -- options read colour tokens, which follow themeKey
  const option = useMemo(() => (show === 'chart' && s.x && s.ys.length ? chartOption(result, s, style, vars) : null), [result, JSON.stringify(s), show, themeKey])
  if (show === 'table') return <Table result={result} />
  if (!option) return <div className="state">Pick a column for X and at least one numeric column for Y.</div>
  return fontsReady ? <Chart theme={themeKey} chart={style.chart} tokens={vars} option={option} label={`${kinds[s.kind]} chart: ${label}`}
    onPick={onPick && s.kind !== 'scatter' ? (v) => onPick(s.x, v) : undefined} /> : null
}
