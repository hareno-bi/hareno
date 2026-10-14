import { useEffect, useRef, useState } from 'react'

// Share a dashboard with people in its company (R1-4b). Positions and groups follow when Kiban 0.2
// lets members list them. Whose rows viewers see (R1-4c): their own, or, for people allowed to
// give away their data on the connection (Kiban connection#data_sharer), the sharer's.
type Person = { id: string; name: string; email: string }
type ShareRow = { member: string; name: string; relation: Relation; data: Data }
type Relation = 'viewer' | 'editor'
type Data = 'viewer' | 'sharer'
const json = { 'Content-Type': 'application/json' }
const relations: Record<Relation, string> = { viewer: 'Can view', editor: 'Can edit' }
const datas: Record<Data, string> = { viewer: 'their own data', sharer: 'my data' }

export default function Share({ id }: { id: string }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const [shares, setShares] = useState<ShareRow[] | null>(null)
  const [q, setQ] = useState('')
  const [found, setFound] = useState<Person[]>([])
  const [relation, setRelation] = useState<Relation>('viewer')
  const [data, setData] = useState<Data>('viewer')
  const [canShareData, setCanShareData] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const call = async (path: string, init?: RequestInit) => {
    setError(null)
    try {
      const r = await fetch(`/api/reports/${id}${path}`, init)
      const body = r.status === 204 ? {} : await r.json()
      if (!r.ok) {
        setError(body.error ?? `HTTP ${r.status}`)
        return null
      }
      return body
    } catch {
      setError('Hareno is not reachable.')
      return null
    }
  }
  const load = async () => {
    const b = await call('/shares')
    if (b) setShares(b.shares)
    const rep = await call('')
    if (rep) setCanShareData(!!rep.canShareData)
  }
  const open = () => {
    dialog.current?.showModal()
    load()
  }

  useEffect(() => {
    if (!q.trim()) {
      setFound([])
      return
    }
    const t = setTimeout(async () => {
      const b = await call(`/people?q=${encodeURIComponent(q.trim())}`)
      if (b) setFound(b.people)
    }, 250)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- search follows the text
  }, [q])

  const share = async (member: string, name: string, rel: Relation, d: Data) => {
    if (await call('/shares', { method: 'POST', headers: json, body: JSON.stringify({ member, name, relation: rel, data: rel === 'editor' ? 'viewer' : d }) })) {
      setQ('')
      load()
    }
  }
  const unshare = async (member: string) => {
    if (await call(`/shares/${encodeURIComponent(member)}`, { method: 'DELETE', headers: json })) load()
  }
  const shared = new Set((shares ?? []).map((s) => s.member))

  return (
    <>
      <button onClick={open}>Share</button>
      <dialog ref={dialog} className="card share" aria-labelledby={`share-${id}`}>
        <header>
          <h3 id={`share-${id}`}>Share dashboard</h3>
          <button className="close" onClick={() => dialog.current?.close()} aria-label="Close">×</button>
        </header>
        <p className="muted">
          People in this dashboard's company. Unless you choose otherwise, each sees only the data their own access allows. Sharing with positions and groups comes with Kiban 0.2.
        </p>
        <div className="share-add">
          <input type="search" placeholder="Find a person by name or email" aria-label="Find a person" value={q} onChange={(e) => setQ(e.target.value)} />
          <select value={relation} onChange={(e) => setRelation(e.target.value as Relation)} aria-label="Access to give">
            {(Object.keys(relations) as Relation[]).map((r) => <option key={r} value={r}>{relations[r]}</option>)}
          </select>
          {canShareData && relation === 'viewer' && <DataPick value={data} onChange={setData} />}
        </div>
        {canShareData && relation === 'viewer' && data === 'sharer' && (
          <p className="note warn">They will see the rows you can see, even rows their own access does not allow.</p>
        )}
        {found.length > 0 && (
          <ul className="share-found">
            {found.map((p) => (
              <li key={p.id}>
                <span>{p.name} <span className="muted">{p.email}</span></span>
                {shared.has(p.id) ? <span className="muted">already shared</span> : <button onClick={() => share(p.id, p.name, relation, data)}>Share</button>}
              </li>
            ))}
          </ul>
        )}
        {q.trim() && found.length === 0 && <p className="muted">No one with a login matches.</p>}
        {error && <p className="sql-error" role="alert">{error}</p>}
        <h3>Shared with</h3>
        {shares === null ? <p className="muted">Loading…</p>
          : shares.length === 0 ? <p className="muted">Only its editors so far.</p>
          : (
            <ul className="share-list">
              {shares.map((s) => (
                <li key={s.member}>
                  <span>{s.name}</span>
                  <select value={s.relation} aria-label={`Access for ${s.name}`} onChange={(e) => share(s.member, s.name, e.target.value as Relation, 'viewer')}>
                    {(Object.keys(relations) as Relation[]).map((r) => <option key={r} value={r}>{relations[r]}</option>)}
                  </select>
                  {s.relation === 'viewer' && (canShareData
                    ? <DataPick value={s.data} onChange={(d) => share(s.member, s.name, 'viewer', d)} label={`Data ${s.name} sees`} />
                    : <span className="muted">sees {datas[s.data]}</span>)}
                  <button onClick={() => unshare(s.member)}>Remove</button>
                </li>
              ))}
            </ul>
          )}
        <p className="muted">Changes take effect within 30 seconds.</p>
      </dialog>
    </>
  )
}

function DataPick({ value, onChange, label = 'Data they see' }: { value: Data; onChange: (d: Data) => void; label?: string }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value as Data)} aria-label={label}>
      {(Object.keys(datas) as Data[]).map((d) => <option key={d} value={d}>Sees {datas[d]}</option>)}
    </select>
  )
}
