import { useEffect, useState } from 'react'
import Charts from './s3/Charts'
import Matrix from './s3/Matrix'
import { useTheme, type Pref } from './s3/theme'
import './s3/s3.css'

const pages = { charts: 'Chart samples', matrix: 'Matrix prototype' } as const
type Page = keyof typeof pages
const fromHash = (): Page => (location.hash === '#/matrix' ? 'matrix' : 'charts')

export default function App() {
  const [page, setPage] = useState<Page>(fromHash)
  const { pref, setPref, mode } = useTheme()
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
          Theme{' '}
          <select value={pref} onChange={(e) => setPref(e.target.value as Pref)}>
            <option value="system">System</option>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
          </select>
        </label>
      </header>
      <main>{page === 'charts' ? <Charts mode={mode} /> : <Matrix />}</main>
    </>
  )
}
