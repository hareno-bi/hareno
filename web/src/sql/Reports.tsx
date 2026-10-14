import { useEffect, useMemo, useState } from 'react'
import { Chart } from '../s3/Chart'
import type { Style, Tokens } from '../s3/theme'
import { chartOption, fit, kinds, type Result, type Spec } from './chart'
import Share from './Share'
import Table from './Table'

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
      <header><h3>Reports</h3><a className="button primary new" href="#/sql">New query</a></header>
      {items.length === 0
        ? <p className="muted">No reports yet. Write a query in the SQL editor and save it.</p>
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

type Published = { revision: number; publishedAt: string; body: { title: string; sql: string; chart: Spec } }

export function ReportView({ id, style, vars, themeKey, fontsReady, onSignedOut }: {
  id: string; style: Style; vars: Tokens; themeKey: string; fontsReady: boolean; onSignedOut: () => void
}) {
  const [report, setReport] = useState<{ published?: Published; canEdit: boolean } | null>(null)
  const [result, setResult] = useState<Result | null>(null)
  const [ranAt, setRanAt] = useState<Date | null>(null)
  const [error, setError] = useState<string | null>(null)

  const run = () => fetch(`/api/reports/${id}/run`, { method: 'POST', headers: json, body: '{}' }).then(async (r) => {
    if (r.status === 401) return onSignedOut()
    const body = await r.json()
    if (!r.ok) return setError(body.error)
    setResult(body)
    setRanAt(new Date())
  }, () => setError('Hareno is not reachable.'))

  useEffect(() => {
    fetch(`/api/reports/${id}`).then(async (r) => {
      if (r.status === 401) return onSignedOut()
      const body = await r.json()
      if (!r.ok) return setError(body.error)
      setReport(body)
      if (body.published) run()
    }, () => setError('Hareno is not reachable.'))
    // eslint-disable-next-line react-hooks/exhaustive-deps -- per report
  }, [id])

  const pub = report?.published
  const spec = pub && result ? fit(pub.body.chart?.kind ? pub.body.chart : { kind: 'bar', x: '', ys: [], split: '' }, result) : null
  // eslint-disable-next-line react-hooks/exhaustive-deps -- options read colour tokens, which follow themeKey
  const option = useMemo(() => (result && spec?.x && spec.ys.length ? chartOption(result, spec, style, vars) : null), [result, pub, themeKey])

  if (error) return <div className="card sql-error" role="alert">{error}</div>
  if (!report) return <div className="state loading">Loading…</div>
  if (!pub) return <p className="note">This report has not been published yet. {report.canEdit && <a href={`#/sql/${id}`}>Open the draft</a>}</p>
  return (
    <div className="report">
      <div className="toolbar">
        <h2>{pub.body.title}</h2>
        <span className="muted">
          Revision {pub.revision}, published {new Date(pub.publishedAt).toLocaleString()}
          {ranAt && ` · data as of ${ranAt.toLocaleTimeString()}`}
        </span>
        <button onClick={run}>Refresh</button>
        {report.canEdit && <Share id={id} />}
        {report.canEdit && <a href={`#/sql/${id}`}>Edit</a>}
      </div>
      {result && (
        <div className="sql-out">
          <section className="card">
            {option && fontsReady && spec
              ? <Chart theme={themeKey} chart={style.chart} tokens={vars} option={option} label={`${kinds[spec.kind]} chart: ${pub.body.title}`} />
              : <div className="state">No chart for this result.</div>}
          </section>
          <section className="card">
            <Table result={result} />
          </section>
        </div>
      )}
    </div>
  )
}
