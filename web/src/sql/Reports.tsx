import { useEffect, useRef, useState } from 'react'
import type { Style, Tokens } from '../s3/theme'
import type { Dash, Result } from './chart'
import Share from './Share'
import TileOutput from './Tile'

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
type TileRun = Partial<Result> & { ranAt?: string; asOf?: string; stale?: boolean; error?: string }
type Run = { tiles: TileRun[]; data: 'viewer' | 'sharer' }

const EVERY = 5000 // dashboards refresh every five seconds while visible (ARCHITECTURE-PLAN §8.1)
const time = (s: string) => new Date(s).toLocaleTimeString()
const when = (s: string) => (isNaN(Date.parse(s)) ? s : new Date(s).toLocaleString())

export function ReportView({ id, style, vars, themeKey, fontsReady, onSignedOut }: {
  id: string; style: Style; vars: Tokens; themeKey: string; fontsReady: boolean; onSignedOut: () => void
}) {
  const [report, setReport] = useState<{ published?: Published; canEdit: boolean; canExport: boolean } | null>(null)
  const [run, setRun] = useState<Run | null>(null)
  const [checked, setChecked] = useState<Date | null>(null) // last successful refresh
  const [failed, setFailed] = useState<string | null>(null) // why the last refresh failed
  const [error, setError] = useState<string | null>(null)
  const busy = useRef(false)

  // refresh asks for every tile; the server answers from its cache unless the data has changed.
  const refresh = async () => {
    if (busy.current) return // never overlap
    busy.current = true
    try {
      const r = await fetch(`/api/reports/${id}/run`, { method: 'POST', headers: json, body: '{}' })
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
    }
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
  if (error) return <div className="card sql-error" role="alert">{error}</div>
  if (!report) return <div className="state loading">Loading…</div>
  if (!pub) return <p className="note">This dashboard has not been published yet. {report.canEdit && <a href={`#/sql/${id}`}>Open the draft</a>}</p>
  return (
    <div className="report">
      <div className="toolbar">
        <h2>{pub.body.title}</h2>
        <span className="muted">Revision {pub.revision}, published {new Date(pub.publishedAt).toLocaleString()}</span>
        {report.canEdit && <Share id={id} />}
        {report.canEdit && <a href={`#/sql/${id}`}>Edit</a>}
      </div>
      <p className="note" role="status">
        {failed
          ? <span className="stale">Could not refresh: {failed} Showing the data from {checked ? checked.toLocaleTimeString() : 'before'}; trying again every 5 seconds.</span>
          : <>Live: checks for new data every 5 seconds{checked && `, last at ${checked.toLocaleTimeString()}`}.</>}
        {' '}
        {run?.data === 'sharer'
          ? `You are seeing the data of the person who shared this dashboard with you.${report.canExport ? ' Downloads hold your own rows only.' : ''}`
          : 'You are seeing your own data: rows outside your access are left out.'}
      </p>
      <div className="grid dash-grid">
        {pub.body.tiles.map((t, i) => {
          const res = run?.tiles[i]
          const name = t.title || `Tile ${i + 1}`
          return (
            <section key={i} className={'card tile' + (t.wide ? ' wide' : '') + (res?.stale ? ' is-stale' : '')} aria-label={name}>
              {t.title && <header><h3>{t.title}</h3></header>}
              {!res ? <div className="state loading">Loading…</div>
                : !res.columns ? <div className="sql-error" role="alert">{res.error}</div>
                : <TileOutput result={res as Result} spec={t.chart} show={t.show} label={name} style={style} vars={vars} themeKey={themeKey} fontsReady={fontsReady} />}
              {res?.columns && (
                <p className="as-of muted">
                  <span>
                    {res.stale && <span className="stale">Stale: could not refresh ({res.error}). </span>}
                    {res.asOf ? `Data as of ${when(res.asOf)}` : `Data as of ${time(res.ranAt!)}`}
                    {res.asOf && ` · queried ${time(res.ranAt!)}`}
                  </span>
                  {report.canExport
                    ? <a href={`/api/reports/${id}/tiles/${i}/csv`} download
                        title={run?.data === 'sharer' ? 'The file holds your own rows, not those shown here' : 'Your rows, fetched fresh'}>Download CSV</a>
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
