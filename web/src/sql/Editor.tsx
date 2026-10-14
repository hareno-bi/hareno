import { useEffect, useMemo, useState, type KeyboardEvent } from 'react'
import { Chart } from '../s3/Chart'
import type { Style, Tokens } from '../s3/theme'
import { chartOption, fit, isNum, kinds, type Kind, type Result, type Spec } from './chart'
import Table from './Table'

// The SQL editor (Phase 2 round 1, R1-3/R1-4): write a query against a connection, see the rows,
// build a chart from them, and save and publish it as a report. Rows are filtered by the database
// for the signed-in user (R1-1/R1-2).
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

type Draft = { title: string; sql: string; chart: Spec }
const json = { 'Content-Type': 'application/json' }

export default function Editor({ reportId, style, vars, themeKey, fontsReady, onSignedOut }: {
  reportId?: string; style: Style; vars: Tokens; themeKey: string; fontsReady: boolean; onSignedOut: () => void
}) {
  const [connections, setConnections] = useState<string[] | null>(null)
  const [connection, setConnection] = useState('')
  const [sql, setSql] = useState(() => (reportId ? '' : remembered()))
  // the saved report this edits: id, the draft version the server has, its published revision,
  // and the draft as last saved (to show unsaved changes)
  const [id, setId] = useState(reportId)
  const [title, setTitle] = useState('')
  const [version, setVersion] = useState(0)
  const [published, setPublished] = useState<number | null>(null)
  const [saved, setSaved] = useState('')
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(!reportId)
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
      const d: Draft = body.draft
      setTitle(d.title)
      setSql(d.sql)
      if (d.chart?.kind) setSpec(d.chart)
      setConnection(body.connection)
      setVersion(body.version)
      setPublished(body.published?.revision ?? null)
      setSaved(JSON.stringify(d))
      setLoaded(true)
    }, () => setError('Hareno is not reachable.'))
  }, [reportId, onSignedOut])

  const draft: Draft = { title: title.trim(), sql, chart: spec }
  const dirty = JSON.stringify(draft) !== saved

  // save stores the draft (creating the report the first time) and returns where it is, or null.
  async function save(): Promise<{ id: string; version: number } | null> {
    if (!draft.title) {
      setError('Give the report a title before saving.')
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
    setSaved(JSON.stringify(draft))
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

  async function run() {
    if (!connection || running) return
    if (!id) {
      try {
        localStorage.setItem('hareno-sql', sql)
      } catch {
        // not remembered; fine
      }
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

  // a saved report runs once it has loaded
  // eslint-disable-next-line react-hooks/exhaustive-deps -- once per load
  useEffect(() => { if (reportId && loaded && connection) run() }, [loaded, connection])

  if (connections && connections.length === 0) {
    return <p className="note">You have no connections you may query with SQL. Ask your administrator for access.</p>
  }
  return (
    <div className="sql">
      <div className="toolbar report-bar">
        <input className="title" aria-label="Report title" placeholder="Untitled report" value={title} maxLength={200}
          onChange={(e) => setTitle(e.target.value)} />
        <button onClick={() => act(save)} disabled={busy || !loaded || (!!id && !dirty)}>Save</button>
        <button className="primary" onClick={publish} disabled={busy || !loaded}>Publish</button>
        <span className="muted" aria-live="polite">
          {!loaded ? 'Loading…' : dirty ? (id ? 'Unsaved changes' : 'Not saved') : 'Saved'}
          {published !== null && <> · <a href={`#/report/${id}`}>published revision {published}</a></>}
        </span>
        <a className="new" href="#/sql">New query</a>
      </div>
      <div className="toolbar">
        <label>
          Connection{' '}
          <select value={connection} onChange={(e) => setConnection(e.target.value)} disabled={!connections || !!id}>
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

