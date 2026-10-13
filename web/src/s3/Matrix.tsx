import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { createColumnHelper, tableFeatures, useTable } from '@tanstack/react-table'
import { useVirtualizer } from '@tanstack/react-virtual'
import { fmtTonnes, fmtWhole, fmtYield, orDash } from './format'

// One row from the server, already in display order with subtotals (see scripts/s3-snapshot.sql):
// [lvl, airline, flight, exportTonnes, exportYield, importTonnes, importYield, tranTonnes, tranYield,
// totalTonnes, totalYield]. lvl 0 = flight, 1 = airline subtotal, 3 = grand total. Yield is
// revenue per kg.
type Row = [number, string | null, string | null, ...(number | null)[]]

const features = tableFeatures({})
const helper = createColumnHelper<typeof features, Row>()
const tonnes = (i: number) =>
  helper.accessor((r) => r[i] as number | null, { id: `c${i}`, header: 'Tonnes', cell: (c) => orDash(c.getValue(), fmtTonnes) })
const yld = (i: number) =>
  helper.accessor((r) => r[i] as number | null, { id: `c${i}`, header: 'Yield / kg', cell: (c) => orDash(c.getValue(), fmtYield) })
const columns = helper.columns([
  helper.accessor((r) => (r[0] === 3 ? 'Total' : r[0] === 1 ? r[1] : r[2]), { id: 'name', header: 'Airline / flight' }),
  helper.group({ id: 'ex', header: 'Export', columns: helper.columns([tonnes(3), yld(4)]) }),
  helper.group({ id: 'im', header: 'Import', columns: helper.columns([tonnes(5), yld(6)]) }),
  helper.group({ id: 'tr', header: 'Transhipment', columns: helper.columns([tonnes(7), yld(8)]) }),
  helper.group({ id: 'tot', header: 'Total', columns: helper.columns([tonnes(9), yld(10)]) }),
])
const ROW = 32
const template = 'minmax(270px, 1.8fr) repeat(8, minmax(90px, 1fr))'

export default function Matrix() {
  const [all, setAll] = useState<Row[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [active, setActive] = useState({ row: 0, col: 0 })
  const [loadMs, setLoadMs] = useState(0)
  useEffect(() => {
    const t = performance.now()
    fetch(import.meta.env.BASE_URL + 's3/matrix.json')
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((rows: Row[]) => {
        setAll(rows)
        setLoadMs(performance.now() - t)
      }, (e: Error) => setError(e.message))
  }, [])

  // Collapsing an airline hides its flights; the airline row keeps its server subtotal.
  const data = useMemo(() => (all ?? []).filter((r) => r[0] !== 0 || !collapsed.has(r[1]!)), [all, collapsed])
  const table = useTable({ features, columns, data, getRowId: (r) => `${r[0]}:${r[1]}:${r[2]}` })
  const rows = table.getRowModel().rows
  const headerGroups = table.getHeaderGroups()
  const nCols = 9

  const scroller = useRef<HTMLDivElement>(null)
  const virt = useVirtualizer({ count: rows.length, getScrollElement: () => scroller.current, estimateSize: () => ROW, overscan: 12 })

  const toggle = useCallback((airline: string) => setCollapsed((s) => {
    const n = new Set(s)
    if (!n.delete(airline)) n.add(airline)
    return n
  }), [])
  const airlines = useMemo(() => (all ?? []).filter((r) => r[0] === 1).map((r) => r[1]!), [all])

  // Keep the active cell in range when rows disappear, and in view when it moves.
  const row = Math.min(active.row, Math.max(rows.length - 1, 0))
  useEffect(() => {
    virt.scrollToIndex(row, { align: 'auto' })
  }, [row, virt])

  // Keyboard model follows the ARIA treegrid pattern: arrows move, Home/End go to row ends
  // (with Ctrl, to the first/last row), PageUp/PageDown move a screen, Enter/Space toggle an
  // airline, and Left/Right on an airline's first cell collapse/expand it.
  const onKey = (e: KeyboardEvent) => {
    const page = Math.max(1, Math.floor((scroller.current?.clientHeight ?? 400) / ROW) - 2)
    const r = rows[row]?.original
    let { col } = active
    let next = row
    switch (e.key) {
      case 'ArrowDown': next++; break
      case 'ArrowUp': next--; break
      case 'PageDown': next += page; break
      case 'PageUp': next -= page; break
      case 'Home': if (e.ctrlKey) next = 0; else col = 0; break
      case 'End': if (e.ctrlKey) next = rows.length - 1; else col = nCols - 1; break
      case 'ArrowRight':
        if (col === 0 && r?.[0] === 1 && collapsed.has(r[1]!)) toggle(r[1]!)
        else col = Math.min(col + 1, nCols - 1)
        break
      case 'ArrowLeft':
        if (col === 0 && r?.[0] === 1 && !collapsed.has(r[1]!)) toggle(r[1]!)
        else if (col === 0 && r?.[0] === 0) next = rows.findIndex((x) => x.original[0] === 1 && x.original[1] === r[1])
        else col = Math.max(col - 1, 0)
        break
      case 'Enter':
      case ' ':
        if (r?.[0] === 1) toggle(r[1]!)
        break
      default:
        return
    }
    e.preventDefault()
    setActive({ row: Math.max(0, Math.min(next, rows.length - 1)), col })
  }

  if (error) return <p className="note">Sample data missing ({error}). Run <code>web/scripts/s3-snapshot.sh</code>.</p>
  if (!all) return <p className="note">Loading flights…</p>
  const activeId = `cell-${row}-${active.col}`
  return (
    <>
      <p className="note">
        Prototype: airline × flow matrix for the fictional Northgate terminal, {fmtWhole(all.length)} rows
        ({fmtWhole(all.filter((r) => r[0] === 0).length)} flights over 24 months), loaded in {fmtWhole(loadMs)} ms.
        Subtotals and the grand total are computed by the database (<code>GROUP BY ROLLUP</code>), with yield
        (revenue per kg) recomputed at every level rather than averaged. Only the visible rows are in the page. Click
        the grid, then use the arrow keys, Page Up/Down, Home/End (Ctrl for first/last row), and Enter or Left/Right on
        an airline to collapse or expand it.
      </p>
      <div className="toolbar">
        <button onClick={() => setCollapsed(new Set())}>Expand all</button>
        <button onClick={() => setCollapsed(new Set(airlines))}>Collapse all</button>
        <span className="muted">{fmtWhole(rows.length)} rows shown</span>
      </div>
      <div
        ref={scroller}
        className="matrix"
        role="treegrid"
        aria-label="Tonnes and yield by airline, flight and flow"
        aria-rowcount={rows.length + headerGroups.length}
        aria-colcount={nCols}
        aria-activedescendant={activeId}
        tabIndex={0}
        onKeyDown={onKey}
      >
        <div className="m-head" role="rowgroup">
          {headerGroups.map((g, gi) => (
            <div key={g.id} role="row" aria-rowindex={gi + 1} className="m-row" style={{ gridTemplateColumns: template }}>
              {g.headers.map((h) => (
                <div key={h.id} role="columnheader" className={'m-cell' + (h.subHeaders.length ? ' m-group' : '') + (h.column.id === 'name' ? '' : ' num')}
                  style={{ gridColumn: `span ${h.colSpan}` }}>
                  {h.isPlaceholder ? null : <table.FlexRender header={h} />}
                </div>
              ))}
            </div>
          ))}
        </div>
        <div role="rowgroup" style={{ height: virt.getTotalSize(), position: 'relative' }}>
          {virt.getVirtualItems().map((v) => {
            const tr = rows[v.index]
            const [lvl, airline] = tr.original
            const isAirline = lvl === 1
            return (
              <div
                key={tr.id}
                role="row"
                aria-rowindex={v.index + headerGroups.length + 1}
                aria-level={lvl === 0 ? 2 : 1}
                aria-expanded={isAirline ? !collapsed.has(airline!) : undefined}
                className={'m-row ' + ['leaf', 'sub', '', 'total'][lvl]}
                style={{ gridTemplateColumns: template, transform: `translateY(${v.start}px)`, height: ROW }}
              >
                {tr.getAllCells().map((c, ci) => (
                  <div
                    key={c.id}
                    id={`cell-${v.index}-${ci}`}
                    role={ci === 0 ? 'rowheader' : 'gridcell'}
                    className={'m-cell' + (ci ? ' num' : '') + (v.index === row && ci === active.col ? ' active' : '')}
                    onClick={() => setActive({ row: v.index, col: ci })}
                  >
                    {ci === 0 && isAirline && (
                      <button className="twisty" tabIndex={-1} aria-hidden onClick={() => toggle(airline!)}>
                        {collapsed.has(airline!) ? '▸' : '▾'}
                      </button>
                    )}
                    <table.FlexRender cell={c} />
                  </div>
                ))}
              </div>
            )
          })}
        </div>
      </div>
    </>
  )
}
