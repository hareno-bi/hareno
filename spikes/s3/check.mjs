// S3 browser checks against the built frontend: matrix virtualization and keyboard model, frame
// times while scrolling, no page errors, and no horizontal page scroll in dark mode and at phone
// width (screenshots saved). Needs Google Chrome at /usr/bin/google-chrome.
import { chromium } from 'playwright-core'
// Usage: node check.mjs [base URL] [screenshot dir]
const base = process.argv[2] ?? 'http://localhost:55450/'
const out = process.argv[3] ?? '.'
const b = await chromium.launch({ executablePath: '/usr/bin/google-chrome', args: ['--no-sandbox'] })
const ok = (c, m) => { console.log((c ? 'ok   ' : 'FAIL ') + m); if (!c) process.exitCode = 1 }

// Matrix: virtualization and keyboard.
const p = await b.newPage({ viewport: { width: 1400, height: 900 } })
const errors = []
p.on('pageerror', (e) => errors.push(e.message))
p.on('console', (m) => m.type() === 'error' && errors.push(m.text()))
await p.goto(base + '#/matrix')
await p.waitForSelector('[role=treegrid] [role=row][aria-level]')
const domRows = await p.locator('[role=row][aria-level]').count()
ok(domRows < 80, `only ${domRows} of 12,004 rows in the DOM`)
const grid = p.locator('[role=treegrid]')
await grid.focus()
const act = () => grid.getAttribute('aria-activedescendant')
await p.keyboard.press('ArrowDown'); await p.keyboard.press('ArrowRight'); await p.keyboard.press('ArrowRight')
ok(await act() === 'cell-1-2', `arrows move the active cell (${await act()})`)
await p.keyboard.press('End'); ok(await act() === 'cell-1-8', `End goes to the last column (${await act()})`)
await p.keyboard.press('Control+End')
ok((await act()).startsWith('cell-12003-'), `Ctrl+End goes to the last row (${await act()})`)
ok(await p.locator('#cell-12003-0').innerText() === 'Total', 'last row is the grand total, in view')
await p.keyboard.press('Control+Home'); await p.keyboard.press('Home')
ok(await act() === 'cell-0-0', `Ctrl+Home, Home go to the first cell (${await act()})`)
await p.keyboard.press('Enter')
ok(await p.locator('.toolbar .muted').innerText() === '8,004 rows shown', 'Enter collapses North (12,004 to 8,004 rows)')
ok(await p.locator('[role=row][aria-level="1"]').first().getAttribute('aria-expanded') === 'false', 'North reports aria-expanded=false')
await p.keyboard.press('ArrowRight')
ok(await p.locator('.toolbar .muted').innerText() === '12,004 rows shown', 'Right expands it again')
await p.keyboard.press('PageDown')
const pd = await act(); ok(+pd.split('-')[1] > 10, `PageDown moves a screen (${pd})`)
await p.keyboard.press('ArrowLeft'); await p.keyboard.press('ArrowLeft')
ok(await act() === 'cell-0-0', `Left on a customer goes to its region (${await act()})`)
// Scroll smoothness: frame times while scrolling the grid by a fixed step every frame.
for (const step of [100, 600]) {
  const r = await p.evaluate(async (step) => {
    const el = document.querySelector('[role=treegrid]'); el.scrollTop = 0
    const d = []; let last = performance.now()
    await new Promise((done) => { let n = 0; const f = (t) => { d.push(t - last); last = t; el.scrollTop += step; if (++n < 240) requestAnimationFrame(f); else done() }; requestAnimationFrame(f) })
    d.sort((a, b) => a - b)
    return { avg: d.reduce((a, b) => a + b) / d.length, p95: d[Math.floor(d.length * 0.95)], max: d[d.length - 1] }
  }, step)
  const msg = `scroll ${step}px/frame (${step * 60}px/s): frame avg ${r.avg.toFixed(1)} ms, p95 ${r.p95.toFixed(1)} ms, max ${r.max.toFixed(1)} ms`
  if (step === 100) ok(r.p95 < 20, msg + ' (60 fps)')
  else console.log('info ' + msg)
}
await p.goto(base + '#/charts'); await p.waitForTimeout(1500)
ok(errors.length === 0, `no page errors (${errors.join('; ')})`)

// Dark mode and phone width screenshots.
for (const [name, vp, theme] of [['charts-dark', { width: 1400, height: 1500 }, 'dark'], ['charts-phone', { width: 390, height: 1600 }, 'light'], ['matrix-dark', { width: 1400, height: 700 }, 'dark']]) {
  const q = await b.newPage({ viewport: vp, colorScheme: theme })
  await q.goto(base + (name.startsWith('matrix') ? '#/matrix' : '#/charts')); await q.waitForTimeout(2000)
  const overflow = await q.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  ok(overflow <= 0, `${name}: no horizontal page scroll (${overflow}px)`)
  await q.screenshot({ path: `${out}/${name}.png` })
}
await b.close()
