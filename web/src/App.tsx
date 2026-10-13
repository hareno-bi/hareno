import { useEffect, useState } from 'react'
import Charts from './s3/Charts'
import Matrix from './s3/Matrix'
import { styles, useTheme, type Pref } from './s3/theme'
import './s3/s3.css'

const pages = { charts: 'Chart samples', matrix: 'Matrix prototype' } as const
type Page = keyof typeof pages
const fromHash = (): Page => (location.hash === '#/matrix' ? 'matrix' : 'charts')

export default function App() {
  const [page, setPage] = useState<Page>(fromHash)
  const { pref, setPref, style, setStyleId, fontsReady, key } = useTheme()
  useEffect(() => {
    const on = () => setPage(fromHash())
    addEventListener('hashchange', on)
    return () => removeEventListener('hashchange', on)
  }, [])
  return (
    <>
      <header className="top">
        <strong className="brand">Hareno</strong>
        <span className="muted">S3 presentation spike</span>
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
      </header>
      <main>{page === 'charts' ? <Charts style={style} themeKey={key} fontsReady={fontsReady} /> : <Matrix />}</main>
    </>
  )
}
