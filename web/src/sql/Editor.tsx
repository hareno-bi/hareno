import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import type { Style, Tokens } from '../s3/theme'
import { aggs, blank, fit, fitMatrix, isNum, kinds, caption, latest, oneValue, roundUp, splits, type Agg, type Dash, type Dataset, type Kind, type Labels, type MatrixSpec, type Pick, type Result, type Show, type Spec, type TileDef } from './chart'
import { examples } from './examples'
import Share from './Share'
import TileOutput from './Tile'

// The dashboard editor (Phase 2 round 1, R1-3/R1-4): tiles, each a SQL query against the
// dashboard's connection shown as a chart or a table, saved and published as a report. Rows are
// filtered by the database for the signed-in user (R1-1/R1-2).
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

const newTile = (sql = ''): TileDef => ({ title: '', sql, chart: blank, show: 'chart', wide: false })
const datasetTile = (d: Dataset): TileDef => ({ ...newTile(), dataset: { dataset: d.name, dimensions: [], measures: [] } })
const ready = (t: TileDef) => (t.dataset ? t.dataset.dimensions.length + t.dataset.measures.length > 0 : !!t.sql.trim())
// a field name as people read it: region_name -> Region name
const label = (n: string) => n.charAt(0).toUpperCase() + n.slice(1).replaceAll('_', ' ')
const firstTile = (): TileDef => {
  let sql = sample
  try {
    sql = localStorage.getItem('hareno-sql') ?? sample
  } catch {
    // not remembered; fine
  }
  return { ...newTile(sql), title: 'Revenue by region', wide: true }
}

// A tile being edited: what is saved, and its last run.
type Edit = TileDef & { key: number; result?: Result; error?: string; running?: boolean; ms?: number; open?: boolean }
let keys = 0
const editing = (t: TileDef, open = false): Edit => ({ ...t, key: ++keys, open })
const saveable = (t: TileDef): TileDef => ({
  title: t.title, sql: t.dataset ? '' : t.sql, show: t.show, wide: t.wide,
  ...(t.dataset ? { dataset: { dataset: t.dataset.dataset, dimensions: t.dataset.dimensions, measures: t.dataset.measures } } : {}),
  chart: t.chart && { kind: t.chart.kind, x: t.chart.x, ys: t.chart.ys, split: t.chart.split, ...(t.chart.max ? { max: t.chart.max } : {}) },
  ...(t.show === 'matrix' && t.matrix ? { matrix: { rows: t.matrix.rows, cols: t.matrix.cols, values: t.matrix.values.map((v) => ({ column: v.column, agg: v.agg })) } } : {}),
})
// same compares drafts whatever their key order (the database keeps its own)
const same = (d: Dash) => JSON.stringify({ title: d.title, tiles: d.tiles.map(saveable), filters: d.filters ?? [] })
const json = { 'Content-Type': 'application/json' }

export default function Editor({ reportId, style, vars, themeKey, fontsReady, onSignedOut }: {
  reportId?: string; style: Style; vars: Tokens; themeKey: string; fontsReady: boolean; onSignedOut: () => void
}) {
  const [connections, setConnections] = useState<string[] | null>(null)
  const [sqlOn, setSqlOn] = useState<string[]>([]) // connections the user may write SQL on
  const [datasets, setDatasets] = useState<Dataset[] | null>(null)
  const [connection, setConnection] = useState('')
  // the saved report this edits: id, the draft version the server has, its published revision,
  // and the draft as last saved (to show unsaved changes)
  const [id, setId] = useState(reportId)
  const [title, setTitle] = useState('')
  const [filters, setFilters] = useState<string[]>([])
  const [tiles, setTiles] = useState<Edit[]>([])
  const [version, setVersion] = useState(0)
  const [published, setPublished] = useState<number | null>(null)
  const [saved, setSaved] = useState('')
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetch('/api/connections').then(async (r) => {
      if (r.status === 401) return onSignedOut()
      const body = await r.json()
      if (!r.ok) return setError(body.error)
      setConnections(body.connections)
      setSqlOn(body.sql ?? [])
      setConnection((c) => c || (body.connections[0] ?? ''))
    }, () => setError('Hareno is not reachable.'))
    fetch('/api/datasets').then(async (r) => {
      const body = await r.json()
      setDatasets(r.ok ? body.datasets : [])
    }, () => setDatasets([]))
  }, [onSignedOut])

  const sqlOk = sqlOn.includes(connection)
  const mine = (datasets ?? []).filter((d) => d.connection === connection)
  // A new dashboard starts with one tile: a SQL sample for SQL users, else a dataset tile.
  useEffect(() => {
    if (reportId || loaded || !connections || !datasets || !connection) return
    setTiles([editing(sqlOk || !mine.length ? firstTile() : { ...datasetTile(mine[0]), wide: true }, true)])
    setLoaded(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once, when rights and datasets are known
  }, [connections, datasets, connection])

  useEffect(() => {
    if (!reportId) return
    fetch(`/api/reports/${reportId}`).then(async (r) => {
      if (r.status === 401) return onSignedOut()
      const body = await r.json()
      if (!r.ok) return setError(body.error)
      if (!body.draft) {
        location.hash = `#/report/${reportId}` // a viewer: show the published report
        return
      }
      const d: Dash = body.draft
      setTitle(d.title)
      setFilters(d.filters ?? [])
      setTiles(d.tiles.map((t) => editing(t)))
      setConnection(body.connection)
      setVersion(body.version)
      setPublished(body.published?.revision ?? null)
      setSaved(same(d))
      setLoaded(true)
    }, () => setError('Hareno is not reachable.'))
  }, [reportId, onSignedOut])

  const draft: Dash = { title: title.trim(), tiles: tiles.map(saveable), filters }
  // filter bar candidates: the text columns of the tiles' results, and those already chosen
  const names: Labels = Object.assign({}, ...tiles.map((t) => t.result?.display))
  const columns = [...new Set([...filters, ...tiles.flatMap((t) => t.result?.columns.filter((_, i) => !isNum(t.result!, i)) ?? [])])]
  const dirty = same(draft) !== saved

  const update = (key: number, p: Partial<Edit>) => setTiles((ts) => ts.map((t) => (t.key === key ? { ...t, ...p } : t)))
  const move = (key: number, by: number) => setTiles((ts) => {
    const i = ts.findIndex((t) => t.key === key), j = i + by
    if (j < 0 || j >= ts.length) return ts
    const out = [...ts];
    [out[i], out[j]] = [out[j], out[i]]
    return out
  })

  async function run(t: Edit) {
    if (!connection || !ready(t)) return
    if (!id && !t.dataset && t.key === tiles[0]?.key) {
      try {
        localStorage.setItem('hareno-sql', t.sql)
      } catch {
        // not remembered; fine
      }
    }
    update(t.key, { running: true, error: undefined })
    const start = performance.now()
    try {
      const sent = JSON.stringify(t.dataset ? { connection, dataset: t.dataset } : { connection, sql: t.sql })
      const r = await fetch('/api/sql', { method: 'POST', headers: json, body: sent })
      if (r.status === 401) return onSignedOut()
      const body = await r.json()
      // an answer to picks changed since (a quicker later run) is dropped: the later one stands
      const current = (x: Edit) => x.key === t.key && (!x.dataset || JSON.stringify({ connection, dataset: x.dataset }) === sent)
      if (!r.ok) return setTiles((ts) => ts.map((x) => (current(x) ? { ...x, running: false, error: body.error ?? `HTTP ${r.status}` } : x)))
      setTiles((ts) => ts.map((x) => (current(x)
        ? { ...x, running: false, result: body, ms: performance.now() - start, chart: fit(x.chart?.kind ? x.chart : blank, body),
            matrix: x.show === 'matrix' ? fitMatrix(x.matrix, body, x.dataset?.measures) : x.matrix }
        : x)))
    } catch {
      update(t.key, { running: false, error: 'Hareno is not reachable.' })
    }
  }

  // every tile runs once the dashboard and its connection are known
  const ran = useRef(false)
  useEffect(() => {
    if (ran.current || !loaded || !connection) return
    ran.current = true
    tiles.forEach(run)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once per load
  }, [loaded, connection])

  // save stores the draft (creating the report the first time) and returns where it is, or null.
  async function save(): Promise<{ id: string; version: number } | null> {
    if (!draft.title) {
      setError('Give the dashboard a title before saving.')
      return null
    }
    const r = await fetch(id ? `/api/reports/${id}` : '/api/reports', {
      method: id ? 'PUT' : 'POST', headers: json,
      body: JSON.stringify(id ? { draft, version } : { connection, draft }),
    })
    if (r.status === 401) {
      onSignedOut()
      return null
    }
    const body = await r.json()
    if (!r.ok) {
      setError(body.error ?? `HTTP ${r.status}`)
      return null
    }
    if (!id) {
      setId(body.id)
      history.replaceState(null, '', `#/sql/${body.id}`)
    }
    setVersion(body.version)
    setSaved(same(draft))
    return { id: id ?? body.id, version: body.version }
  }

  async function act(f: () => Promise<unknown>) {
    setBusy(true)
    setError(null)
    try {
      await f()
    } catch {
      setError('Hareno is not reachable.')
    } finally {
      setBusy(false)
    }
  }

  const publish = () => act(async () => {
    const at = dirty || !id ? await save() : { id, version }
    if (!at) return
    const r = await fetch(`/api/reports/${at.id}/publish`, { method: 'POST', headers: json, body: JSON.stringify({ version: at.version }) })
    const body = await r.json()
    if (!r.ok) return setError(body.error ?? `HTTP ${r.status}`)
    setPublished(body.revision)
  })

  if (connections && connections.length === 0) {
    return <p className="note">You may not build dashboards on any data source. Ask your administrator for access.</p>
  }
  return (
    <div className="sql">
      <div className="toolbar report-bar">
        <input className="title" aria-label="Dashboard title" placeholder="Untitled dashboard" value={title} maxLength={200}
          onChange={(e) => setTitle(e.target.value)} />
        <button onClick={() => act(save)} disabled={busy || !loaded || (!!id && !dirty)}>Save</button>
        <button className="primary" onClick={publish} disabled={busy || !loaded}>Publish</button>
        <span className="muted" aria-live="polite">
          {!loaded ? 'Loading…' : dirty ? (id ? 'Unsaved changes' : 'Not saved') : 'Saved'}
          {published !== null && <> · <a href={`#/report/${id}`}>published revision {published}</a></>}
        </span>
        {id && <Share id={id} />}
        <a className="new" href="#/sql">New dashboard</a>
      </div>
      <div className="toolbar">
        <label>
          Connection{' '}
          <select value={connection} onChange={(e) => setConnection(e.target.value)} disabled={!connections || !!id}>
            {(connections ?? []).map((c) => <option key={c}>{c}</option>)}
          </select>
        </label>
        <button onClick={() => tiles.forEach(run)} disabled={!connection}>Run all</button>
        {sqlOk && <select value="" aria-label="Add an example tile" disabled={!connection || tiles.length >= 24} onChange={(e) => {
          const t = editing(examples[Number(e.target.value)])
          setTiles((ts) => [...ts, t])
          run(t)
        }}>
          <option value="">Add an example tile…</option>
          {examples.map((x, i) => <option key={i} value={i}>{x.title}</option>)}
        </select>}
      </div>
      <fieldset className="toolbar filter-pick">
        <legend>Filter bar</legend>
        {columns.length === 0 && <span className="muted">Run the tiles to choose columns.</span>}
        {columns.map((c) => (
          <label key={c}>
            <input type="checkbox" checked={filters.includes(c)} disabled={!filters.includes(c) && filters.length >= 10}
              onChange={(e) => setFilters((f) => (e.target.checked ? [...f, c] : f.filter((x) => x !== c)))} />
            {' '}{caption(names, c)}
          </label>
        ))}
      </fieldset>
      {error && <div className="card sql-error" role="alert">{error}</div>}
      <div className="grid dash-grid">
        {tiles.map((t, i) => (
          <TileEditor key={t.key} tile={t} first={i === 0} last={i === tiles.length - 1} single={tiles.length === 1}
            update={(p) => update(t.key, p)} move={(by) => move(t.key, by)} run={(x) => run(x ?? t)} datasets={mine} sqlOk={sqlOk}
            remove={() => setTiles((ts) => ts.filter((x) => x.key !== t.key))} connection={connection} onSignedOut={onSignedOut}
            style={style} vars={vars} themeKey={themeKey} fontsReady={fontsReady} />
        ))}
        <div className="add-tile">
          {mine.length > 0 && <button onClick={() => setTiles((ts) => [...ts, editing(datasetTile(mine[0]), true)])} disabled={tiles.length >= 24}>+ Tile from a dataset</button>}
          {sqlOk && <button onClick={() => setTiles((ts) => [...ts, editing(newTile(), true)])} disabled={tiles.length >= 24}>+ SQL tile</button>}
        </div>
      </div>
    </div>
  )
}

function TileEditor({ tile: t, first, last, single, update, move, run, remove, connection, datasets, sqlOk, onSignedOut, style, vars, themeKey, fontsReady }: {
  tile: Edit; first: boolean; last: boolean; single: boolean
  update: (p: Partial<Edit>) => void; move: (by: number) => void; run: (t?: Edit) => void; remove: () => void
  connection: string; datasets: Dataset[]; sqlOk: boolean; onSignedOut: () => void
  style: Style; vars: Tokens; themeKey: string; fontsReady: boolean
}) {
  const name = t.title || 'Untitled tile'
  const pivot = usePivot(t, connection, onSignedOut)
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      run()
    }
  }
  // a dataset tile runs as soon as its picks change
  const pick = (dataset: Pick) => {
    update({ dataset })
    run({ ...t, dataset })
  }
  const show = (s: Show) => update({ show: s, matrix: s === 'matrix' && t.result ? fitMatrix(t.matrix, t.result, t.dataset?.measures) : t.matrix })
  // The tile reads top to bottom: its query, how its result is shown, and the result.
  return (
    <section className={'card tile' + (t.wide ? ' wide' : '')} aria-label={name}>
      <header>
        <input className="tile-title" aria-label="Tile title" placeholder="Untitled tile" value={t.title} maxLength={200}
          onChange={(e) => update({ title: e.target.value })} />
        <span className="tile-tools">
          <select aria-label={`Width of ${name}`} value={t.wide ? 'wide' : 'half'} onChange={(e) => update({ wide: e.target.value === 'wide' })}>
            <option value="half">Half width</option>
            <option value="wide">Full width</option>
          </select>
          <button onClick={() => move(-1)} disabled={first} aria-label={`Move ${name} earlier`} title="Move earlier">↑</button>
          <button onClick={() => move(1)} disabled={last} aria-label={`Move ${name} later`} title="Move later">↓</button>
          <button onClick={remove} disabled={single} aria-label={`Remove ${name}`} title="Remove tile">×</button>
        </span>
      </header>
      <details open={t.open} onToggle={(e) => update({ open: (e.target as HTMLDetailsElement).open })}>
        <summary>{t.dataset ? `Data: ${label(t.dataset.dataset)}` : 'SQL'}{t.result && !t.running && <span className="muted"> · {t.result.rows.length.toLocaleString()} rows{t.result.truncated ? ' (row limit reached)' : ''} · {Math.round(t.ms ?? 0)} ms</span>}</summary>
        {t.dataset
          ? <DataPicker pick={t.dataset} datasets={datasets} setPick={pick} />
          : <>
            <textarea className="sql-input" aria-label={`SQL for ${name}`} spellCheck={false} value={t.sql} placeholder="SELECT …"
              onChange={(e) => update({ sql: e.target.value })} onKeyDown={onKey} readOnly={!sqlOk} />
            {sqlOk && <div className="toolbar">
              <button className="primary" onClick={() => run()} disabled={t.running || !t.sql.trim()} title="Ctrl+Enter">{t.running ? 'Running…' : 'Run'}</button>
            </div>}
          </>}
      </details>
      {t.error && <div className="sql-error" role="alert">{t.error}</div>}
      {t.result && (
        <div className="shape">
          <Field label="Show as">
            <span className="seg">
              {(['chart', 'table', 'matrix'] as Show[]).map((s) => (
                <button key={s} aria-pressed={t.show === s} onClick={() => show(s)}>{{ chart: 'Chart', table: 'Table', matrix: 'Matrix' }[s]}</button>
              ))}
            </span>
          </Field>
          {t.show === 'chart' && <Builder result={t.result} spec={t.chart ?? blank} setSpec={(chart) => update({ chart: fit(chart, t.result!) })} />}
          {t.show === 'matrix' && t.matrix && <MatrixBuilder result={t.result} spec={t.matrix} measures={t.dataset?.measures} setSpec={(matrix) => update({ matrix })} />}
        </div>
      )}
      {t.result && t.show === 'matrix'
        ? pivot.error ? <div className="state">{pivot.error}</div>
          : pivot.result ? <TileOutput result={pivot.result} spec={null} show="matrix" matrix={t.matrix} label={name} style={style} vars={vars} themeKey={themeKey} fontsReady={fontsReady} />
          : <div className="state loading">Totalling…</div>
        : t.result
        ? <TileOutput result={t.result} spec={t.chart} show={t.show} label={name} style={style} vars={vars} themeKey={themeKey} fontsReady={fontsReady} />
        : !t.error && <div className={'state' + (t.running ? ' loading' : '')}>{t.running ? 'Running…' : t.dataset ? 'Pick fields or measures.' : 'Write a query and run it.'}</div>}
    </section>
  )
}

// A field: its name over its control(s).
function Field({ label, children }: { label: string; children: ReactNode }) {
  return <div className="field" role="group" aria-label={label}><span className="field-label">{label}</span><div className="field-controls">{children}</div></div>
}

function Builder({ result, spec, setSpec }: { result: Result; spec: Spec; setSpec: (s: Spec) => void }) {
  const nums = result.columns.filter((_, i) => isNum(result, i))
  const set = (p: Partial<Spec>) => setSpec({ ...spec, ...p })
  const group = useId()
  const pie = spec.kind === 'pie'
  const one = spec.kind === 'kpi' || spec.kind === 'gauge' // the latest row's value
  const single = oneValue(spec.kind) || spec.split !== '' // one value column: slices, one value, or one series per split value
  return (
    <div className="builder">
      <Field label="Type">
        <select aria-label="Chart type" value={spec.kind} onChange={(e) => {
          const kind = e.target.value as Kind
          set({ kind, ys: oneValue(kind) ? spec.ys.slice(0, 1) : spec.ys, split: splits(kind) ? spec.split : '' })
        }}>
          {(Object.keys(kinds) as Kind[]).map((k) => <option key={k} value={k}>{kinds[k]}</option>)}
        </select>
      </Field>
      {!(one && (result.rows.length < 2 || !spec.x)) && <Field label={pie ? 'Slices' : one ? 'Latest by' : 'Categories'}>
        <select aria-label={pie ? 'Slices' : one ? 'Latest by' : 'Categories'} value={spec.x} onChange={(e) => set({ x: e.target.value })}>
          {result.columns.map((c) => <option key={c} value={c}>{caption(result.display, c)}</option>)}
        </select>
      </Field>}
      <Field label={single ? 'Value' : 'Values'}>
        {nums.length === 0 && <span className="muted">no number columns</span>}
        {nums.map((c) => (
          <label key={c} className="pick">
            <input type={single ? 'radio' : 'checkbox'} name={group} checked={spec.ys.includes(c)}
              onChange={(e) => set({ ys: single ? [c] : e.target.checked ? [...spec.ys, c] : spec.ys.filter((y) => y !== c) })} />
            {caption(result.display, c)}
          </label>
        ))}
      </Field>
      {spec.kind === 'gauge' && result.display?.[spec.ys[0]]?.format !== 'percent' && (
        <Field label="Maximum">
          <input type="number" aria-label="Gauge maximum" min={0} className="num-input" value={spec.max ?? ''}
            placeholder={String(roundUp(latest(result, spec).value ?? 0))}
            onChange={(e) => set({ max: e.target.value === '' ? undefined : Number(e.target.value) })} />
        </Field>
      )}
      {splits(spec.kind) && (
        <Field label="Series by">
          <select aria-label="Series by" value={spec.split} onChange={(e) => set({ split: e.target.value, ys: spec.ys.slice(0, 1) })}>
            <option value="">(none)</option>
            {result.columns.filter((c) => c !== spec.x).map((c) => <option key={c} value={c}>{caption(result.display, c)}</option>)}
          </select>
        </Field>
      )}
    </div>
  )
}

// usePivot runs a matrix tile's query totalled as the matrix asks, whenever its rows or matrix change.
function usePivot(t: Edit, connection: string, onSignedOut: () => void) {
  const [out, setOut] = useState<{ result?: Result; error?: string }>({})
  const spec = t.show === 'matrix' && t.result && t.matrix?.rows.length && t.matrix.values.length ? JSON.stringify(t.matrix) : ''
  useEffect(() => {
    if (!spec) return
    let current = true
    setOut({})
    const source = t.dataset ? { dataset: t.dataset } : { sql: t.sql }
    fetch('/api/sql', { method: 'POST', headers: json, body: JSON.stringify({ connection, ...source, matrix: JSON.parse(spec) }) }).then(async (r) => {
      if (r.status === 401) return onSignedOut()
      const body = await r.json()
      if (current) setOut(r.ok ? { result: body } : { error: body.error ?? `HTTP ${r.status}` })
    }, () => current && setOut({ error: 'Hareno is not reachable.' }))
    return () => { current = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reruns with the tile's result, not as its SQL is typed
  }, [spec, t.result, connection])
  return spec ? out : { error: 'Choose at least one value.' }
}

// MatrixBuilder: on a dataset tile (measures given), values are the tile's measures as defined.
function MatrixBuilder({ result, spec, measures, setSpec }: { result: Result; spec: MatrixSpec; measures?: string[]; setSpec: (m: MatrixSpec) => void }) {
  const nums = result.columns.filter((_, i) => isNum(result, i))
  const set = (p: Partial<MatrixSpec>) => setSpec({ ...spec, ...p })
  const used = [...spec.rows, ...spec.cols]
  const free = (keep?: string) => result.columns.filter((c) => !measures?.includes(c) && (c === keep || !used.includes(c)))
  // a column moved to rows or columns stops being a value
  const place = (rows: string[], cols: string[]) => set({ rows, cols, values: spec.values.filter((v) => !rows.includes(v.column) && !cols.includes(v.column)) })
  const value = (c: string) => spec.values.find((v) => v.column === c)
  return (
    <div className="builder">
      <Field label="Rows">
        {spec.rows.map((r, i) => (
          <span key={i} className="level">
            {i > 0 && <span className="muted" aria-hidden>›</span>}
            <select aria-label={`Row level ${i + 1}`} value={r} onChange={(e) => place(spec.rows.map((x, j) => (j === i ? e.target.value : x)), spec.cols)}>
              {free(r).map((c) => <option key={c} value={c}>{caption(result.display, c)}</option>)}
            </select>
            {spec.rows.length > 1 && <button className="x" aria-label={`Remove row level ${caption(result.display, r)}`} title="Remove" onClick={() => place(spec.rows.filter((_, j) => j !== i), spec.cols)}>×</button>}
          </span>
        ))}
        {spec.rows.length < 3 && free().length > 0 && (
          <select aria-label="Add a row level" className="add" value="" onChange={(e) => place([...spec.rows, e.target.value], spec.cols)}>
            <option value="">+ Level</option>
            {free().map((c) => <option key={c} value={c}>{caption(result.display, c)}</option>)}
          </select>
        )}
      </Field>
      <Field label="Columns">
        <select aria-label="Columns" value={spec.cols[0] ?? ''} onChange={(e) => place(spec.rows, e.target.value ? [e.target.value] : [])}>
          <option value="">(none)</option>
          {free(spec.cols[0]).map((c) => <option key={c} value={c}>{caption(result.display, c)}</option>)}
        </select>
      </Field>
      <Field label="Values">
        {result.columns.filter((c) => !used.includes(c) && (!measures || measures.includes(c))).map((c) => {
          const v = value(c)
          const options = (measures ? [''] : nums.includes(c) ? Object.keys(aggs) : ['count', 'min', 'max']) as (Agg | '')[]
          return (
            <span key={c} className={'pick' + (v ? ' on' : '')}>
              <label>
                <input type="checkbox" checked={!!v} disabled={!v && spec.values.length >= 6}
                  onChange={(e) => set({ values: e.target.checked ? [...spec.values, { column: c, agg: options[0] }] : spec.values.filter((x) => x.column !== c) })} />
                {caption(result.display, c)}
              </label>
              {v && !measures && (
                <select aria-label={`How to total ${c}`} value={v.agg}
                  onChange={(e) => set({ values: spec.values.map((x) => (x.column === c ? { ...x, agg: e.target.value as Agg } : x)) })}>
                  {options.map((a) => <option key={a} value={a}>{a && aggs[a]}</option>)}
                </select>
              )}
            </span>
          )
        })}
      </Field>
    </div>
  )
}

// DataPicker chooses a dataset tile's dataset, the fields it groups by, and its measures.
function DataPicker({ pick, datasets, setPick }: { pick: Pick; datasets: Dataset[]; setPick: (p: Pick) => void }) {
  const d = datasets.find((x) => x.name === pick.dataset)
  const toggle = (list: string[], n: string, on: boolean) => (on ? [...list, n] : list.filter((x) => x !== n))
  const byName = (ns: string[]) => [...ns].sort((a, b) => caption(d?.display, a).localeCompare(caption(d?.display, b)))
  return (
    <div className="builder data-pick">
      <Field label="Dataset">
        <select aria-label="Dataset" value={pick.dataset} onChange={(e) => setPick({ dataset: e.target.value, dimensions: [], measures: [] })}>
          {!d && <option value={pick.dataset}>{label(pick.dataset)} (not available)</option>}
          {datasets.map((x) => <option key={x.name} value={x.name}>{label(x.name)}</option>)}
        </select>
      </Field>
      {d && (
        <>
          <Field label="Group by">
            {byName(d.dimensions).map((n) => (
              <label key={n} className="pick">
                <input type="checkbox" checked={pick.dimensions.includes(n)} onChange={(e) => setPick({ ...pick, dimensions: toggle(pick.dimensions, n, e.target.checked) })} />
                {caption(d.display, n)}
              </label>
            ))}
          </Field>
          <Field label="Measures">
            {byName(d.measures).map((n) => (
              <label key={n} className="pick">
                <input type="checkbox" checked={pick.measures.includes(n)} onChange={(e) => setPick({ ...pick, measures: toggle(pick.measures, n, e.target.checked) })} />
                {caption(d.display, n)}
              </label>
            ))}
          </Field>
        </>
      )}
    </div>
  )
}
