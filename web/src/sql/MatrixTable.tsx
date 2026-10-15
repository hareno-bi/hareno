import { aggs, number, caption, type MatrixSpec, type Result, type Value } from './chart'

// MatrixTable lays out a matrix tile's result: one row per row group (its subtotal first, then
// its members), the grand total last, and a column per column value and value, with a total.
// The server sent every total (internal/sqlapi/matrix.go): rows are
// [hareno_level, ...row columns, ...column columns, ...values], and hareno_level has a bit per
// grouping column, set where the row totals over it. Clicking a row or column value filters by it.
//
// ponytail: every row is in the page (results stop at the row limit); virtualize when matrices grow.
const TOTAL = '\u0000total'
const fmt = (v: Value) => (v === null ? '' : typeof v === 'number' ? v.toLocaleString(undefined, { maximumFractionDigits: 2 }) : String(v))
const text = (v: Value) => (v === null ? '(null)' : String(v))

function compare(a: Value, b: Value) {
  if (a === b) return 0
  if (a === null) return 1
  if (b === null) return -1
  if (typeof a === 'number' && typeof b === 'number') return a - b
  return String(a).localeCompare(String(b), undefined, { numeric: true })
}

// a group comes before its members; the grand total (no columns) goes last
function compareKeys(a: Value[], b: Value[]) {
  if (!a.length || !b.length) return b.length - a.length
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    if (i >= a.length) return -1
    if (i >= b.length) return 1
    const c = compare(a[i], b[i])
    if (c) return c
  }
  return 0
}

export default function MatrixTable({ result, spec, onPick }: {
  result: Result; spec: MatrixSpec; onPick?: (column: string, value: string) => void
}) {
  const nR = spec.rows.length, nC = spec.cols.length, nV = spec.values.length
  if (result.columns.length !== 1 + nR + nC + nV || result.columns[0] !== 'hareno_level') {
    return <div className="state">Run the tile to see the matrix.</div>
  }
  const rolled = (lvl: number, j: number) => (lvl >> (nR + nC - 1 - j)) & 1
  const rowKeys = new Map<string, Value[]>()
  const colKeys = new Map<string, Value>()
  const cells = new Map<string, Value[]>()
  for (const r of result.rows) {
    const lvl = r[0] as number
    let depth = 0
    while (depth < nR && !rolled(lvl, depth)) depth++
    const key = r.slice(1, 1 + depth)
    const rk = JSON.stringify(key)
    const ck = nC && !rolled(lvl, nR) ? JSON.stringify(r[1 + nR]) : TOTAL
    rowKeys.set(rk, key)
    if (ck !== TOTAL) colKeys.set(ck, r[1 + nR])
    cells.set(rk + '\u0001' + ck, r.slice(1 + nR + nC))
  }
  const rows = [...rowKeys].sort((a, b) => compareKeys(a[1], b[1]))
  const cols: [string, Value][] = [...[...colKeys].sort((a, b) => compare(a[1], b[1])), [TOTAL, null]]
  const d = result.display
  const name = (c: string) => caption(d, c)
  const labels = spec.values.map((v) => (v.agg ? `${aggs[v.agg]} of ${name(v.column)}` : name(v.column)))
  const show = spec.values.map((v) => (d?.[v.column] ? (x: Value) => (typeof x === 'number' ? number(d, v.column)(x) : fmt(x)) : fmt))
  const template = `minmax(180px, 1.6fr) repeat(${cols.length * nV}, minmax(96px, 1fr))`
  const minWidth = 180 + cols.length * nV * 96
  const pick = (column: string, v: Value) => onPick && v !== null && (
    <button className="link" onClick={() => onPick(column, text(v))} title={`Filter by ${name(column)} ${text(v)}`}>{text(v)}</button>
  )
  const corner = spec.rows.map(name).join(' / ') + (nC ? `  ·  ${name(spec.cols[0])} →` : '')
  if (result.rows.length === 0) return <div className="state empty">The query returned no rows.</div>
  return (
    <div className="matrix sql-table pivot" role="table" aria-label={`${labels.join(', ')} by ${[...spec.rows, ...spec.cols].map(name).join(', ')}`} tabIndex={0}>
      <div className="m-head" role="rowgroup">
        {nC > 0 && (
          <div className="m-row" role="row" style={{ gridTemplateColumns: template, minWidth }}>
            <div role="columnheader" className="m-cell">{corner}</div>
            {cols.map(([k, v]) => (
              <div key={k} role="columnheader" className="m-cell m-group" style={{ gridColumn: `span ${nV}` }}>
                {k === TOTAL ? 'Total' : pick(spec.cols[0], v) || text(v)}
              </div>
            ))}
          </div>
        )}
        {(nC === 0 || nV > 1) && (
          <div className="m-row" role="row" style={{ gridTemplateColumns: template, minWidth }}>
            <div role="columnheader" className="m-cell">{nC ? '' : corner}</div>
            {cols.flatMap(([k]) => labels.map((l, i) => <div key={k + i} role="columnheader" className="m-cell num">{l}</div>))}
          </div>
        )}
      </div>
      <div role="rowgroup">
        {rows.map(([rk, key]) => {
          const depth = key.length
          return (
            <div key={rk} role="row" className={'m-row ' + (depth === 0 ? 'total' : depth < nR ? 'sub' : '')} style={{ gridTemplateColumns: template, minWidth }}>
              <div role="rowheader" className="m-cell" style={{ paddingLeft: 10 + Math.max(depth - 1, 0) * 18 }}>
                {depth === 0 ? 'Total' : pick(spec.rows[depth - 1], key[depth - 1]) || text(key[depth - 1])}
              </div>
              {cols.flatMap(([ck]) => {
                const vs = cells.get(rk + '\u0001' + ck)
                return spec.values.map((_, i) => <div key={ck + i} role="cell" className="m-cell num">{vs ? show[i](vs[i]) : ''}</div>)
              })}
            </div>
          )
        })}
      </div>
      {result.truncated && <p className="note stale">The result reached the row limit: some groups and totals are missing.</p>}
    </div>
  )
}
