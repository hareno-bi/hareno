import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import type { EChartsCoreOption } from 'echarts/core'
import { useVirtualizer } from '@tanstack/react-virtual'
import { Chart } from '../s3/Chart'
import { fmtCompact } from '../s3/format'
import { treatment, type Style, type Tokens } from '../s3/theme'

// The SQL editor (Phase 2 round 1, R1-3): write a query against a connection, see the rows, and
// build a chart from them. Rows are filtered by the database for the signed-in user (R1-1/R1-2).
type Value = string | number | boolean | null
type Result = { columns: string[]; rows: Value[][]; truncated: boolean }
const kinds = { bar: 'Column', hbar: 'Bar', line: 'Line', area: 'Area', stacked: 'Stacked column', pie: 'Pie', scatter: 'Scatter' } as const
type Kind = keyof typeof kinds
type Spec = { kind: Kind; x: string; ys: string[]; split: string }

const sample = `-- Revenue by region and month. Rows outside your data scope are filtered by the database.
SELECT r.region_name AS region,
       to_char(o.ordered_at, 'YYYY-MM') AS month,
       round(sum(li.quantity * li.unit_price * (1 - coalesce(li.discount, 0))), 2) AS revenue
FROM mart.fact_order o
JOIN mart.dim_customer c USING (customer_id)
JOIN mart.dim_region r USING (region_id)
JOIN mart.fact_order_line li USING (order_id)
WHERE o.status = 'complete'
GROUP BY 1, 2
ORDER BY 2, 1`

function remembered() {
  try {
    return localStorage.getItem('hareno-sql') ?? sample
  } catch {
    return sample
  }
}

export default function Editor({ style, vars, themeKey, fontsReady, onSignedOut }: {
  style: Style; vars: Tokens; themeKey: string; fontsReady: boolean; onSignedOut: () => void
}) {
  const [connections, setConnections] = useState<string[] | null>(null)
  const [connection, setConnection] = useState('')
  const [sql, setSql] = useState(remembered)
  const [result, setResult] = useState<Result | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [running, setRunning] = useState(false)
  const [ms, setMs] = useState(0)
  const [spec, setSpec] = useState<Spec>({ kind: 'bar', x: '', ys: [], split: '' })

  useEffect(() => {
    fetch('/api/connections').then(async (r) => {
      if (r.status === 401) return onSignedOut()
      const body = await r.json()
      if (!r.ok) return setError(body.error)
      setConnections(body.connections)
      setConnection(body.connections[0] ?? '')
    }, () => setError('Hareno is not reachable.'))
  }, [onSignedOut])

  async function run() {
    if (!connection || running) return
    try {
      localStorage.setItem('hareno-sql', sql)
    } catch {
      // not remembered; fine
    }
    setRunning(true)
    setError(null)
    const t = performance.now()
    try {
      const r = await fetch('/api/sql', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ connection, sql }) })
      if (r.status === 401) return onSignedOut()
      const body = await r.json()
      if (!r.ok) {
        setError(body.error ?? `HTTP ${r.status}`)
        return
      }
      setMs(performance.now() - t)
      setResult(body)
      setSpec((s) => fit(s, body))
    } catch {
      setError('Hareno is not reachable.')
    } finally {
      setRunning(false)
    }
  }

  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      run()
    }
  }

  // eslint-disable-next-line react-hooks/exhaustive-deps -- options read colour tokens, which follow themeKey
  const option = useMemo(() => (result && spec.x && spec.ys.length ? chartOption(result, spec, style, vars) : null), [result, spec, themeKey])

  if (connections && connections.length === 0) {
    return <p className="note">You have no connections you may query with SQL. Ask your administrator for access.</p>
  }
  return (
    <div className="sql">
      <div className="toolbar">
        <label>
          Connection{' '}
          <select value={connection} onChange={(e) => setConnection(e.target.value)} disabled={!connections}>
            {(connections ?? []).map((c) => <option key={c}>{c}</option>)}
          </select>
        </label>
        <button className="primary" onClick={run} disabled={!connection || running}>{running ? 'Running…' : 'Run'}</button>
        <span className="muted">Ctrl+Enter runs</span>
        <span className="muted status" aria-live="polite">
          {result && !running && `${result.rows.length.toLocaleString()} rows${result.truncated ? ' (row limit reached, more not shown)' : ''} · ${Math.round(ms)} ms`}
        </span>
      </div>
      <textarea className="sql-input" aria-label="SQL query" spellCheck={false} value={sql}
        onChange={(e) => setSql(e.target.value)} onKeyDown={onKey} />
      {error && <div className="card sql-error" role="alert">{error}</div>}
      {result && (
        <div className="sql-out">
          <section className="card">
            <header><h3>Chart</h3></header>
            <Builder result={result} spec={spec} setSpec={setSpec} />
            {option && fontsReady
              ? <Chart theme={themeKey} chart={style.chart} tokens={vars} option={option} label={`${kinds[spec.kind]} chart of ${spec.ys.join(', ')} by ${spec.x}`} />
              : <div className="state">Pick a column for X and at least one numeric column for Y.</div>}
          </section>
          <section className="card">
            <header><h3>Rows</h3></header>
            <Table result={result} />
          </section>
        </div>
      )}
    </div>
  )
}

const isNum = (r: Result, i: number) => r.rows.some((row) => row[i] !== null) && r.rows.every((row) => row[i] === null || typeof row[i] === 'number')

// fit keeps the chart choices that still apply to a new result, and picks defaults for the rest:
// X the first text column, Y the first number column after it.
function fit(s: Spec, r: Result): Spec {
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

function Builder({ result, spec, setSpec }: { result: Result; spec: Spec; setSpec: (s: Spec) => void }) {
  const nums = result.columns.filter((_, i) => isNum(result, i))
  const set = (p: Partial<Spec>) => setSpec({ ...spec, ...p })
  const single = spec.kind === 'pie' || spec.split !== '' // one value column: slices, or one series per split value
  return (
    <div className="builder">
      <label>Type <select value={spec.kind} onChange={(e) => {
        const kind = e.target.value as Kind
        set({ kind, ys: kind === 'pie' ? spec.ys.slice(0, 1) : spec.ys, split: kind === 'pie' ? '' : spec.split })
      }}>
        {(Object.keys(kinds) as Kind[]).map((k) => <option key={k} value={k}>{kinds[k]}</option>)}
      </select></label>
      <label>{spec.kind === 'pie' ? 'Slices' : 'X'} <select value={spec.x} onChange={(e) => set({ x: e.target.value })}>
        {result.columns.map((c) => <option key={c}>{c}</option>)}
      </select></label>
      <fieldset>
        <legend>{spec.kind === 'pie' ? 'Value' : 'Y'}</legend>
        {nums.length === 0 && <span className="muted">no number columns</span>}
        {nums.map((c) => (
          <label key={c}>
            <input type={single ? 'radio' : 'checkbox'} name="y" checked={spec.ys.includes(c)}
              onChange={(e) => set({ ys: single ? [c] : e.target.checked ? [...spec.ys, c] : spec.ys.filter((y) => y !== c) })} />
            {' '}{c}
          </label>
        ))}
      </fieldset>
      {spec.kind !== 'pie' && (
        <label>Series by <select value={spec.split} onChange={(e) => set({ split: e.target.value, ys: spec.ys.slice(0, 1) })}>
          <option value="">(none)</option>
          {result.columns.filter((c) => c !== spec.x).map((c) => <option key={c}>{c}</option>)}
        </select></label>
      )}
    </div>
  )
}

const label = (v: Value) => (v === null ? '(null)' : String(v))
const MAX_SERIES = 24

// chartOption turns rows into an ECharts option. With "Series by", rows become one series per
// value of that column; rows with the same X (and series) are added together.
function chartOption(r: Result, s: Spec, style: Style, vars: Tokens): EChartsCoreOption {
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

const ROW = 30

function Table({ result }: { result: Result }) {
  const scroller = useRef<HTMLDivElement>(null)
  const virt = useVirtualizer({ count: result.rows.length, getScrollElement: () => scroller.current, estimateSize: () => ROW, overscan: 16 })
  const nums = result.columns.map((_, i) => isNum(result, i))
  const template = `repeat(${result.columns.length}, minmax(120px, 1fr))`
  const minWidth = result.columns.length * 120
  if (result.rows.length === 0) return <div className="state empty">The query returned no rows.</div>
  return (
    <div ref={scroller} className="matrix sql-table" role="table" aria-rowcount={result.rows.length + 1} tabIndex={0}>
      <div className="m-head" role="rowgroup">
        <div className="m-row" role="row" style={{ gridTemplateColumns: template, minWidth }}>
          {result.columns.map((c, i) => <div key={i} role="columnheader" className={'m-cell' + (nums[i] ? ' num' : '')}>{c}</div>)}
        </div>
      </div>
      <div role="rowgroup" style={{ height: virt.getTotalSize(), position: 'relative' }}>
        {virt.getVirtualItems().map((v) => (
          <div key={v.index} role="row" aria-rowindex={v.index + 2} className="m-row abs"
            style={{ gridTemplateColumns: template, minWidth, height: ROW, transform: `translateY(${v.start}px)` }}>
            {result.rows[v.index].map((val, i) => (
              <div key={i} role="cell" className={'m-cell' + (nums[i] ? ' num' : '') + (val === null ? ' muted' : '')}>
                {val === null ? 'null' : String(val)}
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  )
}
