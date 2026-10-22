import { useEffect, useRef, useState } from 'react'
import type { Style, Tokens } from '../s3/theme'
import { caption, type Dash, type Labels, type Result } from './chart'
import Share from './Share'
import TileOutput, { picks } from './Tile'

// Saved reports (Phase 2 round 1, R1-4): the list, and a published report as its viewers see it.
// Every write sends Content-Type: application/json: the API refuses cookie-authenticated writes
// without it (cross-site request protection, internal/signin).
const json = { 'Content-Type': 'application/json' }
type Item = { id: string; title: string; connection: string; published: number | null; updatedAt: string; canEdit: boolean }

export function Reports({ onSignedOut }: { onSignedOut: () => void }) {
  const [items, setItems] = useState<Item[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const load = () => fetch('/api/reports').then(async (r) => {
    if (r.status === 401) return onSignedOut()
    const body = await r.json()
    if (!r.ok) return setError(body.error)
    setItems(body.reports)
  }, () => setError('Hareno is not reachable.'))
  // eslint-disable-next-line react-hooks/exhaustive-deps -- once
  useEffect(() => { load() }, [])

  const remove = async (it: Item) => {
    if (!confirm(`Delete "${it.title}"? Everyone it is shared with loses it too.`)) return
    const r = await fetch(`/api/reports/${it.id}`, { method: 'DELETE', headers: json })
    if (!r.ok) return setError((await r.json()).error)
    load()
  }

  if (error) return <div className="card sql-error" role="alert">{error}</div>
  if (!items) return <div className="state loading">Loading…</div>
  return (
    <section className="card reports">
      <header><h3>Dashboards</h3><a className="button primary new" href="#/sql">New dashboard</a></header>
      {items.length === 0
        ? <p className="muted">No dashboards yet. Build one in the dashboard editor and save it.</p>
        : (
          <table>
            <thead><tr><th>Title</th><th>Connection</th><th>Status</th><th>Updated</th><th /></tr></thead>
            <tbody>
              {items.map((it) => (
                <tr key={it.id}>
                  <td><a href={it.published ? `#/report/${it.id}` : `#/sql/${it.id}`}>{it.title}</a></td>
                  <td>{it.connection}</td>
                  <td>{it.published ? `Published, revision ${it.published}` : 'Draft, not published'}</td>
                  <td>{new Date(it.updatedAt).toLocaleString()}</td>
                  <td className="actions">
                    {it.canEdit && <a href={`#/sql/${it.id}`}>Edit</a>}
                    {it.canEdit && <button onClick={() => remove(it)}>Delete</button>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
    </section>
  )
}

type Published = { revision: number; publishedAt: string; body: Dash }
// One tile's result: rows (perhaps the last good ones, marked stale) or only an error.
type TileRun = Partial<Result> & { ranAt?: string; asOf?: string; stale?: boolean; error?: string; filtered?: string[]; dated?: boolean; drill?: Drill }
// Drill: a tile drilling down a hierarchy (server: drill.go): its own level and those below, and
// how far down it is (levels[at] is shown)
type Drill = { levels: string[]; at: number }
type Run = { tiles: TileRun[]; data: 'viewer' | 'sharer'; options: Record<string, string[]>; dates: boolean }
// Filters: column -> the one value kept. They narrow every tile with that column (server: filters.go).
export type Filters = Record<string, string>
// A date range: a span counted back from today, or from one day to another, both included. It
// narrows every dataset tile on its dataset's date (server: dates.go).
export type Dates = { last: string } | { from: string; to: string }
const lasts = { '7d': 'Last 7 days', '30d': 'Last 30 days', '3m': 'Last 3 months', '12m': 'Last 12 months', year: 'This year', lastyear: 'Last year' } as const

const EVERY = 5000 // dashboards refresh every five seconds while visible (ARCHITECTURE-PLAN §8.1)
const time = (s: string) => new Date(s).toLocaleTimeString()
const when = (s: string) => (isNaN(Date.parse(s)) ? s : new Date(s).toLocaleString())

// embedded: shown in another site's frame (Embed): no editing or sharing, the title only if asked;
// filters: the filters it starts with
export function ReportView({ id, style, vars, themeKey, fontsReady, onSignedOut, embedded, title = true, filters: initial = {}, dates: initialDates = null }: {
  id: string; style: Style; vars: Tokens; themeKey: string; fontsReady: boolean; onSignedOut: () => void
  embedded?: boolean; title?: boolean; filters?: Filters; dates?: Dates | null
}) {
  const [report, setReport] = useState<{ published?: Published; canEdit: boolean; canExport: boolean } | null>(null)
  const [run, setRun] = useState<Run | null>(null)
  const [checked, setChecked] = useState<Date | null>(null) // last successful refresh
  const [failed, setFailed] = useState<string | null>(null) // why the last refresh failed
  const [error, setError] = useState<string | null>(null)
  const [filters, setFilters] = useState<Filters>(initial)
  const busy = useRef(false)
  const again = useRef(false) // filters changed during a refresh: refresh once more after it
  const current = useRef(filters) // for the poll, which keeps the first render's refresh
  current.current = filters
  const [dates, setDates] = useState<Dates | null>(initialDates)
  const currentDates = useRef(dates)
  currentDates.current = dates
  const [drill, setDrill] = useState<Record<number, string[]>>({}) // by tile: the values clicked, top down
  const currentDrill = useRef(drill)
  currentDrill.current = drill

  // refresh asks for every tile; the server answers from its cache unless the data has changed.
  const refresh = async () => {
    if (busy.current) {
      again.current = true // never overlap
      return
    }
    busy.current = true
    const sent = current.current, sentDates = currentDates.current, sentDrill = currentDrill.current
    try {
      const f = Object.fromEntries(Object.entries(sent).map(([c, v]) => [c, [v]]))
      const r = await fetch(`/api/reports/${id}/run`, { method: 'POST', headers: json, body: JSON.stringify({ filters: f, dates: sentDates, drill: sentDrill }) })
      if (r.status === 401) return onSignedOut()
      const body = await r.json()
      if (r.status === 404) return setError(body.error) // unshared or deleted: stop showing it
      if (!r.ok) return setFailed(body.error ?? `HTTP ${r.status}`)
      setRun(body)
      setChecked(new Date())
      setFailed(null)
    } catch {
      setFailed('Hareno is not reachable.')
    } finally {
      busy.current = false
      if (again.current || sent !== current.current || sentDates !== currentDates.current || sentDrill !== currentDrill.current) {
        again.current = false
        refresh()
      }
    }
  }
  const filter = (column: string, value: string | null) => {
    const next = { ...current.current }
    if (value === null) delete next[column]
    else next[column] = value
    current.current = next
    setFilters(next)
    refresh()
  }
  const range = (d: Dates | null) => {
    currentDates.current = d
    setDates(d)
    refresh()
  }
  // go shows tile i drilled down through values (none: its own level)
  const go = (i: number, values: string[]) => {
    const next = { ...currentDrill.current, [i]: values }
    if (!values.length) delete next[i]
    currentDrill.current = next
    setDrill(next)
    refresh()
  }
  // a click on a chart filters by what was clicked, or clears that filter if it was already set
  const pick = (column: string, value: string) => {
    if (value !== '(null)') filter(column, current.current[column] === value ? null : value)
  }

  useEffect(() => {
    fetch(`/api/reports/${id}`).then(async (r) => {
      if (r.status === 401) return onSignedOut()
      const body = await r.json()
      if (!r.ok) return setError(body.error)
      setReport(body)
      if (body.published) refresh()
    }, () => setError('Hareno is not reachable.'))
    // eslint-disable-next-line react-hooks/exhaustive-deps -- per report
  }, [id])

  // Poll while the tab is visible; catch up as soon as it is shown again.
  const live = !!report?.published && !error
  useEffect(() => {
    if (!live) return
    const tick = () => { if (!document.hidden) refresh() }
    const t = setInterval(tick, EVERY)
    document.addEventListener('visibilitychange', tick)
    return () => {
      clearInterval(t)
      document.removeEventListener('visibilitychange', tick)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- refresh reads only id
  }, [live, id])

  const pub = report?.published
  const names: Labels = Object.assign({}, ...(run?.tiles ?? []).map((t) => t.display)) // dataset columns' labels
  if (error) return <div className="card sql-error" role="alert">{error}</div>
  if (!report) return <div className="state loading">Loading…</div>
  if (!pub) return <p className="note">This dashboard has not been published yet. {report.canEdit && <a href={`#/sql/${id}`}>Open the draft</a>}</p>
  return (
    <div className="report">
      {embedded
        ? title && <div className="toolbar"><h2>{pub.body.title}</h2></div>
        : <div className="toolbar">
          <h2>{pub.body.title}</h2>
          <span className="muted">Revision {pub.revision}, published {new Date(pub.publishedAt).toLocaleString()}</span>
          {report.canEdit && <Share id={id} />}
          {report.canEdit && <a href={`#/sql/${id}`}>Edit</a>}
        </div>}
      <p className="note" role="status">
        {failed
          ? <span className="stale">Could not refresh: {failed} Showing the data from {checked ? checked.toLocaleTimeString() : 'before'}; trying again every 5 seconds.</span>
          : <>Live: checks for new data every 5 seconds{checked && `, last at ${checked.toLocaleTimeString()}`}.</>}
        {' '}
        {run?.data === 'sharer'
          ? `You are seeing the data of the person who shared this dashboard with you.${report.canExport ? ' Downloads hold your own rows only.' : ''}`
          : 'You are seeing your own data: rows outside your access are left out.'}
      </p>
      <FilterBar columns={[...new Set([...(pub.body.filters ?? []), ...Object.keys(filters)])]} filters={filters}
        options={run?.options ?? {}} offered={pub.body.filters ?? []} names={names} set={filter}
        dates={run?.dates ? dates : undefined} setDates={range} />
      <div className="grid dash-grid">
        {pub.body.tiles.map((t, i) => {
          const res = run?.tiles[i]
          const name = t.title || `Tile ${i + 1}`
          const not = res?.columns ? Object.keys(filters).filter((c) => !res.filtered?.includes(c)) : []
          const undated = !!res?.columns && !!dates && !res.dated
          const mine = Object.fromEntries((res?.filtered ?? []).map((c) => [c, [filters[c]]]))
          const down = drill[i] ?? [], dr = res?.drill
          const q = new URLSearchParams({ ...(res?.filtered?.length ? { filters: JSON.stringify(mine) } : {}), ...(dates && res?.dated ? { dates: JSON.stringify(dates) } : {}),
            ...(down.length ? { drill: JSON.stringify(down) } : {}) }).toString()
          // drilled, the chart shows the level below in place of the tile's own
          const spec = dr && t.chart ? { ...t.chart, x: t.chart.x === dr.levels[0] ? dr.levels[dr.at] : t.chart.x, split: t.chart.split === dr.levels[0] ? dr.levels[dr.at] : t.chart.split } : t.chart
          const drills = !!dr && dr.at < dr.levels.length - 1 && t.show === 'chart' && !!spec && picks(spec.kind) && spec.x === dr.levels[dr.at]
          const csv = `/api/reports/${id}/tiles/${i}/csv` + (q ? `?${q}` : '')
          return (
            <section key={i} className={'card tile' + (t.wide ? ' wide' : '') + (res?.stale ? ' is-stale' : '')} aria-label={name}>
              {t.title && <header><h3>{t.title}</h3></header>}
              {not.length > 0 && <p className="unfiltered muted">Not filtered by {not.map((c) => caption(names, c)).join(', ')}: this tile has no such column.</p>}
              {undated && <p className="unfiltered muted">Not filtered by date: {t.dataset ? 'its dataset has no date set' : 'a SQL tile has no date set'}.</p>}
              {(down.length > 0 || drills) && (
                <nav className="drill muted" aria-label="Drill-down">
                  {down.length > 0 && <>
                    <button onClick={() => go(i, down.slice(0, -1))} title="Go up a level">↑ Up</button>
                    <button className="link" onClick={() => go(i, [])}>All</button>
                    {down.map((v, k) => <span key={k}> › {k < down.length - 1 ? <button className="link" onClick={() => go(i, down.slice(0, k + 1))}>{v}</button> : <b>{v}</b>}</span>)}
                  </>}
                  {drills && <span>{down.length > 0 && ' · '}Click a {caption(names, dr.levels[dr.at]).toLowerCase()} to see it by {caption(names, dr.levels[dr.at + 1]).toLowerCase()}.</span>}
                </nav>
              )}
              {!res ? <div className="state loading">Loading…</div>
                : !res.columns ? <div className="sql-error" role="alert">{res.error}</div>
                : <TileOutput result={res as Result} spec={spec} show={t.show} matrix={t.matrix} label={name} style={style} vars={vars} themeKey={themeKey} fontsReady={fontsReady}
                    onPick={drills ? (_, v) => { if (v !== '(null)') go(i, [...down, v]) } : pick} />}
              {res?.columns && (
                <p className="as-of muted">
                  <span>
                    {res.stale && <span className="stale">Stale: could not refresh ({res.error}). </span>}
                    {res.asOf ? `Data as of ${when(res.asOf)}` : `Data as of ${time(res.ranAt!)}`}
                    {res.asOf && ` · queried ${time(res.ranAt!)}`}
                  </span>
                  {report.canExport
                    ? <a href={csv} download
                        title={(t.show === 'matrix' ? 'The rows the matrix totals. ' : '') + (run?.data === 'sharer' ? 'The file holds your own rows, not those shown here' : 'Your rows, fetched fresh')}>Download CSV</a>
                    : <button disabled title="You may not download data from this source">Download CSV</button>}
                </p>
              )}
            </section>
          )
        })}
      </div>
    </div>
  )
}

// FilterBar shows a choice per filter column: the bar's own, and any added by clicking a chart
// (those can be removed). Choices are the values in the viewer's own results.
// dates is the date range, or undefined when no tile has a date a range would narrow.
function FilterBar({ columns, filters, options, offered, names, set, dates, setDates }: {
  columns: string[]; filters: Filters; options: Record<string, string[]>; offered: string[]; names: Labels
  set: (column: string, value: string | null) => void; dates?: Dates | null; setDates: (d: Dates | null) => void
}) {
  if (!columns.length && dates === undefined) return <p className="note muted">Click a bar or slice in a chart, or a matrix row, to filter the other tiles by it.</p>
  return (
    <div className="toolbar filter-bar" role="group" aria-label="Filters">
      {dates !== undefined && <DateRange dates={dates} set={setDates} />}
      {columns.map((c) => (
        <span key={c} className={'filter' + (filters[c] !== undefined ? ' on' : '')}>
          <label>{caption(names, c)}{' '}
            <select value={filters[c] ?? ''} onChange={(e) => set(c, e.target.value === '' ? null : e.target.value)}>
              <option value="">All</option>
              {[...new Set([...(options[c] ?? []), ...(filters[c] !== undefined ? [filters[c]] : [])])].map((v) => <option key={v}>{v}</option>)}
            </select>
          </label>
          {!offered.includes(c) && <button onClick={() => set(c, null)} aria-label={`Remove the ${caption(names, c)} filter`} title="Remove filter">×</button>}
        </span>
      ))}
      {Object.keys(filters).length > 0 && <button onClick={() => Object.keys(filters).forEach((c) => set(c, null))}>Clear filters</button>}
      <span className="muted">Tip: click a bar, slice or matrix row to filter by it.</span>
    </div>
  )
}

// DateRange picks the dashboard's date range: a span back from today, or two days.
function DateRange({ dates, set }: { dates: Dates | null; set: (d: Dates | null) => void }) {
  const [between, setBetween] = useState(dates && 'from' in dates ? dates : null) // being picked
  const choice = dates === null ? '' : 'last' in dates ? dates.last : 'between'
  const pick = (d: { from: string; to: string }) => {
    setBetween(d)
    if (d.from && d.to && d.from <= d.to) set(d)
  }
  return (
    <span className={'filter' + (dates ? ' on' : '')}>
      <label>Dates{' '}
        <select value={between ? 'between' : choice} onChange={(e) => {
          const v = e.target.value
          const today = new Date().toISOString().slice(0, 10)
          if (v === 'between') return setBetween(dates && 'from' in dates ? dates : { from: today, to: today })
          setBetween(null)
          set(v ? { last: v } : null)
        }}>
          <option value="">All dates</option>
          {Object.entries(lasts).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          <option value="between">Between…</option>
        </select>
      </label>
      {between && <>
        <input type="date" aria-label="Dates from" value={between.from} max={between.to} onChange={(e) => pick({ ...between, from: e.target.value })} />
        <span aria-hidden>to</span>
        <input type="date" aria-label="Dates to" value={between.to} min={between.from} onChange={(e) => pick({ ...between, to: e.target.value })} />
      </>}
    </span>
  )
}

