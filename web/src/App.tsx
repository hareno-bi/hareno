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
function useUser() {
  const [user, setUser] = useState<string | null | undefined>(undefined)
  useEffect(() => {
    fetch('/api/me').then((r) => (r.ok ? r.json() : null)).then((b) => setUser(b?.name ?? null), () => setUser(null))
  }, [])
  const signOut = async () => {
    const r = await fetch('/auth/logout', { method: 'POST' })
    location.href = (await r.json()).redirect
  }
  return { user, setUser, signOut }
}

export default function App() {
  const [page, setPage] = useState<Page>(fromHash)
  const { pref, setPref, style, setStyleId, fontsReady, vars, key } = useTheme()
  const { user, setUser, signOut } = useUser()
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
            : user === null ? <SignIn />
            : <Editor style={style} vars={vars} themeKey={key} fontsReady={fontsReady} onSignedOut={signedOut} />)}
        </main>
      </div>
    </>
  )
}

function SignIn() {
  return (
    <div className="card signin">
      <h2>Sign in to use the SQL editor</h2>
      <p className="muted">You sign in with your Kiban account. What you can query, and which rows you see, comes from your Kiban access.</p>
      <a className="button primary" href="/auth/login">Sign in</a>
    </div>
  )
}
