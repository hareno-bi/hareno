import { useEffect, useState } from 'react'
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
type Run = { tiles: (Result & { error?: undefined } | { error: string })[]; data: 'viewer' | 'sharer' }

export function ReportView({ id, style, vars, themeKey, fontsReady, onSignedOut }: {
  id: string; style: Style; vars: Tokens; themeKey: string; fontsReady: boolean; onSignedOut: () => void
}) {
  const [report, setReport] = useState<{ published?: Published; canEdit: boolean } | null>(null)
  const [run, setRun] = useState<Run | null>(null)
  const [ranAt, setRanAt] = useState<Date | null>(null)
  const [error, setError] = useState<string | null>(null)

  const refresh = () => fetch(`/api/reports/${id}/run`, { method: 'POST', headers: json, body: '{}' }).then(async (r) => {
    if (r.status === 401) return onSignedOut()
    const body = await r.json()
    if (!r.ok) return setError(body.error)
    setRun(body)
    setRanAt(new Date())
  }, () => setError('Hareno is not reachable.'))

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

  const pub = report?.published
  if (error) return <div className="card sql-error" role="alert">{error}</div>
  if (!report) return <div className="state loading">Loading…</div>
  if (!pub) return <p className="note">This dashboard has not been published yet. {report.canEdit && <a href={`#/sql/${id}`}>Open the draft</a>}</p>
  return (
    <div className="report">
      <div className="toolbar">
        <h2>{pub.body.title}</h2>
        <span className="muted">
          Revision {pub.revision}, published {new Date(pub.publishedAt).toLocaleString()}
          {ranAt && ` · data as of ${ranAt.toLocaleTimeString()}`}
        </span>
        <button onClick={refresh}>Refresh</button>
        {report.canEdit && <Share id={id} />}
        {report.canEdit && <a href={`#/sql/${id}`}>Edit</a>}
      </div>
      <p className="note" role="status">
        {run?.data === 'sharer'
          ? 'You are seeing the data of the person who shared this dashboard with you.'
          : 'You are seeing your own data: rows outside your access are left out.'}
      </p>
      <div className="grid dash-grid">
        {pub.body.tiles.map((t, i) => {
          const res = run?.tiles[i]
          const name = t.title || `Tile ${i + 1}`
          return (
            <section key={i} className={'card tile' + (t.wide ? ' wide' : '')} aria-label={name}>
              {t.title && <header><h3>{t.title}</h3></header>}
              {!res ? <div className="state loading">Loading…</div>
                : res.error !== undefined ? <div className="sql-error" role="alert">{res.error}</div>
                : <TileOutput result={res} spec={t.chart} show={t.show} label={name} style={style} vars={vars} themeKey={themeKey} fontsReady={fontsReady} />}
            </section>
          )
        })}
      </div>
    </div>
  )
}
