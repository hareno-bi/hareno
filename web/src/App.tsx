import { useCallback, useEffect, useState, type CSSProperties } from 'react'
import Charts from './s3/Charts'
import Matrix from './s3/Matrix'
import { styles, useTheme, type Pref } from './s3/theme'
import Editor from './sql/Editor'
import { Embed } from './sql/Embed'
import { PrintView, ReportView, Reports } from './sql/Reports'
import './s3/s3.css'

const pages = { sql: 'Dashboard editor', reports: 'Dashboards', charts: 'Chart samples', matrix: 'Matrix prototype' } as const
type Page = keyof typeof pages | 'report' | 'embed' | 'print'
// #/sql, #/sql/<report id>, #/reports, #/report/<report id>, #/charts, #/matrix,
// #/embed/<report id>?<options>: a published dashboard alone, for a frame in another site (Embed), and
// #/print/<report id>?<options>: every page of one, laid out for paper (PrintView)
const fromHash = (): { page: Page; id?: string; params: URLSearchParams } => {
  const [path, query] = location.hash.slice(2).split('?')
  const [p, id] = path.split('/')
  const params = new URLSearchParams(query)
  return p in pages || p === 'report' || p === 'embed' || p === 'print' ? { page: p as Page, id, params } : { page: 'sql', params }
}

// user: undefined while asking, null when signed out (or no API behind a static demo).
// demo: test accounts, listed only when the API's configuration has them (dev and demo stacks);
// links: dashboards in the menu, by title (the same).
type Link = { label: string; title: string }
type Account = { user: string; password: string; remarks: string[]; company: string; via: string; sql: boolean; rows: string; shareData: boolean; export: boolean; datasets: boolean }
function useUser() {
  const [user, setUser] = useState<string | null | undefined>(undefined)
  const [demo, setDemo] = useState<Account[]>([])
  const [links, setLinks] = useState<Link[]>([])
  useEffect(() => {
    fetch('/api/me').then((r) => (r.ok ? r.json() : null)).then((b) => {
      setUser(b?.name ?? null)
      setDemo(b?.demo ?? [])
      setLinks(b?.links ?? [])
    }, () => setUser(null))
  }, [])
  const signOut = async () => {
    const r = await fetch('/auth/logout', { method: 'POST' })
    location.href = (await r.json()).redirect
  }
  return { user, setUser, demo, links, signOut }
}

// openLink shows the published dashboard with a link's title, among those the viewer may see.
async function openLink(l: Link) {
  const r = await fetch('/api/reports')
  const found = r.ok ? ((await r.json()).reports as { id: string; title: string; published: number | null }[]).find((x) => x.title === l.title && x.published) : undefined
  if (found) location.hash = `#/report/${found.id}`
  else alert(r.status === 401 ? 'Sign in first.' : `“${l.title}” is not published, or not shared with you.`)
}

export default function App() {
  const [{ page, id, params }, setRoute] = useState(fromHash)
  const [nav, setNav] = useState(0) // remounts the page on every navigation, e.g. New query from a saved one
  const embed = page === 'embed'
  const theme = params.get('theme')
  const { pref, setPref, style, setStyleId, fontsReady, vars, key } = useTheme(embed ? {
    pref: theme === 'light' || theme === 'dark' ? theme : undefined, style: params.get('style') ?? undefined } : page === 'print' ? { pref: 'light' } : {})
  const { user, setUser, demo, links, signOut } = useUser()
  const signedOut = useCallback(() => setUser(null), [setUser])
  useEffect(() => {
    const on = () => {
      setRoute(fromHash())
      setNav((n) => n + 1)
    }
    addEventListener('hashchange', on)
    return () => removeEventListener('hashchange', on)
  }, [])
  if (embed) return (
    <div className="dash embed" style={vars as CSSProperties}>
      <main>
        {id && <Embed id={id} params={params} user={user} style={style} vars={vars} themeKey={key} fontsReady={fontsReady} />}
      </main>
    </div>
  )
  return (
    <>
      <header className="top">
        <strong className="brand">Hareno</strong>
        <nav>
          {(Object.keys(pages) as (keyof typeof pages)[]).map((p) => (
            <a key={p} href={`#/${p}`} aria-current={p === page || (p === 'reports' && page === 'report') ? 'page' : undefined}>{pages[p]}</a>
          ))}
          {user && links.map((l) => <a key={l.title} href="#/reports" onClick={(e) => { e.preventDefault(); openLink(l) }}>{l.label}</a>)}
        </nav>
        <label className="theme">
          Style{' '}
          <select value={style.id} onChange={(e) => setStyleId(e.target.value)}>
            {styles.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        </label>
        <label>
          Theme{' '}
          <select value={pref} onChange={(e) => setPref(e.target.value as Pref)}>
            <option value="system">System</option>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
          </select>
        </label>
        {user && <span className="user">{user} <button onClick={signOut}>Sign out</button></span>}
      </header>
      <div className="dash" style={vars as CSSProperties}>
        <main>
          {page === 'charts' && <Charts style={style} vars={vars} themeKey={key} fontsReady={fontsReady} />}
          {page === 'matrix' && <Matrix />}
          {(page === 'sql' || page === 'reports' || page === 'report' || page === 'print') && (user === undefined ? null
            : user === null ? <SignIn demo={demo} />
            : <>
              {page === 'sql' && <Editor key={nav} reportId={id} style={style} vars={vars} themeKey={key} fontsReady={fontsReady} onSignedOut={signedOut} />}
              {page === 'reports' && <Reports key={nav} onSignedOut={signedOut} />}
              {page === 'report' && id && <ReportView key={nav} id={id} style={style} vars={vars} themeKey={key} fontsReady={fontsReady} onSignedOut={signedOut} />}
              {page === 'print' && id && <PrintView key={nav} id={id} params={params} style={style} vars={vars} themeKey={key} fontsReady={fontsReady} onSignedOut={signedOut} />}
            </>)}
        </main>
      </div>
    </>
  )
}

const yes = (v: boolean) => (v ? <span className="yes">Yes</span> : <span className="muted">No</span>)

function SignIn({ demo }: { demo: Account[] }) {
  return (
    <div className="card signin">
      <h2>Sign in to use Hareno</h2>
      <p className="muted">You sign in with your Kiban account. What you can query, and which rows you see, comes from your Kiban access.</p>
      <a className="button primary" href="/auth/login">Sign in</a>
      {demo.length > 0 && (
        <div className="accounts">
          <h3>Test accounts</h3>
          <p className="muted">Each sees different data from the same dashboard. The user name is filled in for you; paste the password.</p>
          <div className="scroll-x">
            <table>
              <thead>
                <tr>
                  <th>User</th><th>Password</th><th>Company</th><th>Access</th>
                  <th>Builds dashboards</th><th>Rows they see</th><th>May share their data</th><th>May download CSV</th><th>Remarks</th><th />
                </tr>
              </thead>
              <tbody>
                {demo.map((a) => (
                  <tr key={a.user}>
                    <td><strong>{a.user}</strong></td>
                    <td><code>{a.password}</code> <Copy text={a.password} /></td>
                    <td>{a.company}</td>
                    <td>{a.via}</td>
                    <td>{a.sql ? 'Yes, with SQL' : a.datasets ? 'Yes, from datasets' : yes(false)}</td>
                    <td>{a.rows === 'None' ? <span className="muted">None</span> : a.rows}</td>
                    <td>{yes(a.shareData)}</td>
                    <td>{yes(a.export)}</td>
                    <td><ul className="remarks">{a.remarks?.map((r) => <li key={r}>{r}</li>)}</ul></td>
                    <td><a className="button" href={`/auth/login?user=${encodeURIComponent(a.user)}`}>Sign in as {a.user}</a></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <SampleData />
        </div>
      )}
    </div>
  )
}

// SampleData shows every row of the test data, so testers can tell which rows each account should see.
function SampleData() {
  const [data, setData] = useState<{ columns: string[]; rows: (string | number | null)[][] } | null>(null)
  useEffect(() => {
    fetch('/api/demo-data').then((r) => (r.ok ? r.json() : null)).then(setData, () => null)
  }, [])
  if (!data) return null
  const region = data.columns.indexOf('region')
  const num = data.columns.map((_, j) => data.rows.some((r) => typeof r[j] === 'number'))
  return (
    <>
      <h3>Sample data</h3>
      <p className="muted">
        All {data.rows.length} order lines in the test data. alice sees the North rows, bob all of them, erin and carol none
        (unless something is shared with erin). In the background, an order line is added every 5 seconds for
        30 seconds, then removed one by one (order ids from 1000000), so published dashboards update by themselves.
      </p>
      <div className="scroll-x">
        <table className="sample">
          <thead><tr>{data.columns.map((c, j) => <th key={c} className={num[j] ? 'num' : undefined}>{c.replace('_', ' ')}</th>)}</tr></thead>
          <tbody>
            {data.rows.map((row, i) => (
              <tr key={i} className={region >= 0 && row[region] !== data.rows[i - 1]?.[region] ? 'group' : undefined}>
                {row.map((v, j) => <td key={j} className={num[j] ? 'num' : undefined}>{v === null ? <span className="muted">null</span> : String(v)}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  )
}

function Copy({ text }: { text: string }) {
  const [done, setDone] = useState(false)
  const copy = () => navigator.clipboard?.writeText(text).then(() => {
    setDone(true)
    setTimeout(() => setDone(false), 1500)
  }, () => null)
  // clipboard needs a secure context; on the plain-HTTP demo the button hides and the text is selectable
  if (!navigator.clipboard) return null
  return <button className="copy" onClick={copy}>{done ? 'Copied' : 'Copy'}</button>
}
