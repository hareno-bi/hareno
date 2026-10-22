import { useEffect } from 'react'
import type { Style, Tokens } from '../s3/theme'
import { ReportView, type Dates, type Filters } from './Reports'

// Embed is a published dashboard alone, for a frame in another site (#/embed/<id>?<options>):
//   theme=light|dark, style=<style id>  how it looks (else the viewer's own choice)
//   title=0                              hides the dashboard's title
//   filter.<column>=<value>              filters it starts with (row security still applies)
//   dates=3m, or dates=2026-02-01..2026-03-31  the date range it starts with (7d, 30d, 3m, 12m, year, lastyear)
// The viewer signs in as anywhere in Hareno. Signed out, it first tries a silent sign-in (Kiban
// already knows the browser, e.g. through Windows sign-in), then offers a sign-in window.
// It posts its height to the host page ({ type: 'hareno:height', height }), to size the frame.
// Which sites may frame it is set where Hareno is served: Content-Security-Policy frame-ancestors.
export function Embed({ id, params, user, style, vars, themeKey, fontsReady }: {
  id: string; params: URLSearchParams; user: string | null | undefined; style: Style; vars: Tokens; themeKey: string; fontsReady: boolean
}) {
  const tried = params.get('signin') // set by the server after a silent sign-in: 'needed' or 'done'
  // silent signs in without showing a page if Kiban already knows the browser; back here either way
  const silent = () => {
    const rest = new URLSearchParams([...params].filter(([k]) => k !== 'signin')).toString()
    location.href = `/auth/login?silent=1&return=${encodeURIComponent(`#/embed/${id}${rest ? `?${rest}` : ''}`)}`
  }
  useEffect(() => {
    if (user === null && !tried) silent()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- silent reads only id and params
  }, [user, tried])
  useEffect(() => {
    const signedIn = (e: MessageEvent) => { if (e.origin === location.origin && e.data === 'hareno:signed-in') location.reload() }
    addEventListener('message', signedIn)
    return () => removeEventListener('message', signedIn)
  }, [])
  useEffect(() => {
    if (window.parent === window) return
    const tell = () => window.parent.postMessage({ type: 'hareno:height', height: document.documentElement.scrollHeight }, '*')
    const ro = new ResizeObserver(tell)
    ro.observe(document.body)
    return () => ro.disconnect()
  }, [])

  if (user === undefined || (user === null && !tried)) return <div className="state loading">Loading…</div>
  if (user === null) return (
    <div className="card signin embed-signin">
      <h2>Sign in to see this dashboard</h2>
      {tried === 'done'
        ? <p className="sql-error">You signed in, but this browser kept Hareno from remembering it inside this page (it blocks cookies in
          frames from another site). <a href={`/#/report/${id}`} target="_blank" rel="noopener">Open the dashboard in Hareno</a> instead.</p>
        : <p className="muted">You sign in with your Kiban account. Which rows you see comes from your access.</p>}
      <button className="primary" onClick={() => {
        if (!window.open('/auth/login?popup=1', 'hareno-signin', 'width=480,height=640')) alert('Allow pop-ups for this page to sign in, or open the dashboard in Hareno.')
      }}>Sign in</button>
    </div>
  )
  const [from, to] = (params.get('dates') ?? '').split('..')
  const dates: Dates | null = to ? { from, to } : from ? { last: from } : null
  const filters: Filters = Object.fromEntries([...params].filter(([k]) => k.startsWith('filter.')).map(([k, v]) => [k.slice(7), v]))
  return (
    <>
      {/* the session ended while open (e.g. it expired): sign in silently again */}
      <ReportView id={id} style={style} vars={vars} themeKey={themeKey} fontsReady={fontsReady} onSignedOut={silent}
        embedded title={params.get('title') !== '0'} filters={filters} dates={dates} />
      <p className="embed-foot muted"><a href={`/#/report/${id}`} target="_blank" rel="noopener">Open in Hareno</a></p>
    </>
  )
}
