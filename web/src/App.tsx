import { useCallback, useEffect, useState, type CSSProperties } from 'react'
import Charts from './s3/Charts'
import Matrix from './s3/Matrix'
import { styles, useTheme, type Pref } from './s3/theme'
import Editor from './sql/Editor'
import './s3/s3.css'

const pages = { sql: 'SQL editor', charts: 'Chart samples', matrix: 'Matrix prototype' } as const
type Page = keyof typeof pages
const fromHash = (): Page => {
  const p = location.hash.slice(2)
  return p in pages ? (p as Page) : 'sql'
}

// user: undefined while asking, null when signed out (or no API behind a static demo).
// demo: test accounts, listed only when the API's configuration has them (dev and demo stacks).
type Account = { user: string; password: string; note: string }
function useUser() {
  const [user, setUser] = useState<string | null | undefined>(undefined)
  const [demo, setDemo] = useState<Account[]>([])
  useEffect(() => {
    fetch('/api/me').then((r) => (r.ok ? r.json() : null)).then((b) => {
      setUser(b?.name ?? null)
      setDemo(b?.demo ?? [])
    }, () => setUser(null))
  }, [])
  const signOut = async () => {
    const r = await fetch('/auth/logout', { method: 'POST' })
    location.href = (await r.json()).redirect
  }
  return { user, setUser, demo, signOut }
}

export default function App() {
  const [page, setPage] = useState<Page>(fromHash)
  const { pref, setPref, style, setStyleId, fontsReady, vars, key } = useTheme()
  const { user, setUser, demo, signOut } = useUser()
  const signedOut = useCallback(() => setUser(null), [setUser])
  useEffect(() => {
    const on = () => setPage(fromHash())
    addEventListener('hashchange', on)
    return () => removeEventListener('hashchange', on)
  }, [])
  return (
    <>
      <header className="top">
        <strong className="brand">Hareno</strong>
        <nav>
          {(Object.keys(pages) as Page[]).map((p) => (
            <a key={p} href={`#/${p}`} aria-current={p === page ? 'page' : undefined}>{pages[p]}</a>
          ))}
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
          {page === 'sql' && (user === undefined ? null
            : user === null ? <SignIn demo={demo} />
            : <>
              {demo.length > 0 && <TestAccounts demo={demo} />}
              <Editor style={style} vars={vars} themeKey={key} fontsReady={fontsReady} onSignedOut={signedOut} />
            </>)}
        </main>
      </div>
    </>
  )
}

function SignIn({ demo }: { demo: Account[] }) {
  return (
    <div className="card signin">
      <h2>Sign in to use the SQL editor</h2>
      <p className="muted">You sign in with your Kiban account. What you can query, and which rows you see, comes from your Kiban access.</p>
      <a className="button primary" href="/auth/login">Sign in</a>
      {demo.length > 0 && (
        <div className="accounts">
          <h3>Test accounts</h3>
          <p className="muted">Each sees different data from the same query. The user name is filled in for you; paste the password.</p>
          <table>
            <thead><tr><th>User</th><th>Password</th><th>What they see</th><th /></tr></thead>
            <tbody>
              {demo.map((a) => (
                <tr key={a.user}>
                  <td><strong>{a.user}</strong></td>
                  <td><code>{a.password}</code> <Copy text={a.password} /></td>
                  <td>{a.note}</td>
                  <td><a className="button" href={`/auth/login?user=${encodeURIComponent(a.user)}`}>Sign in as {a.user}</a></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

// TestAccounts reminds a signed-in tester who else to try.
function TestAccounts({ demo }: { demo: Account[] }) {
  return (
    <p className="note test-accounts">
      Test accounts (sign out to switch):{' '}
      {demo.map((a, i) => <span key={a.user}>{i > 0 && ' · '}<strong>{a.user}</strong> / <code>{a.password}</code>: {a.note}</span>)}
    </p>
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
