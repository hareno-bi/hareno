import { useRef } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { isNum, number, caption, type Result } from './chart'

const ROW = 30

// whole: every row at once, no scrolling (for paper); else a short table shrinks to its rows
export default function Table({ result, whole }: { result: Result; whole?: boolean }) {
  const scroller = useRef<HTMLDivElement>(null)
  const virt = useVirtualizer({ count: result.rows.length, getScrollElement: () => scroller.current, estimateSize: () => ROW, overscan: 16 })
  const nums = result.columns.map((_, i) => isNum(result, i))
  const d = result.display
  const show = result.columns.map((c) => (d?.[c] ? number(d, c) : null)) // dataset columns: formatted
  // text columns share the width by how long their values run (sampled), numbers take an even share
  const long = result.columns.map((c, i) => Math.max(c.length, ...result.rows.slice(0, 200).map((r) => String(r[i] ?? '').length)))
  // on paper there is no scrolling sideways: narrower columns, the text wraps
  const least = whole ? 60 : 120
  const template = result.columns.map((_, i) => (nums[i] ? `minmax(${whole ? 60 : 90}px, 1fr)` : `minmax(${least}px, ${Math.min(4, Math.max(1, long[i] / 14)).toFixed(2)}fr)`)).join(' ')
  const minWidth = result.columns.length * least
  if (result.rows.length === 0) return <div className="state empty">The query returned no rows.</div>
  return (
    <div ref={scroller} className="matrix sql-table" role="table" aria-rowcount={result.rows.length + 1} tabIndex={0}
      style={whole ? { height: 'auto', maxHeight: 'none', overflow: 'visible' } : { maxHeight: (result.rows.length + 1) * ROW + 4 }}>
      <div className="m-head" role="rowgroup">
        <div className={'m-row' + (whole ? ' wrap' : '')} role="row" style={{ gridTemplateColumns: template, minWidth }}>
          {result.columns.map((c, i) => <div key={i} role="columnheader" className={'m-cell' + (nums[i] ? ' num' : '')}>{caption(d, c)}</div>)}
        </div>
      </div>
      {whole ? (
        <div role="rowgroup">
          {result.rows.map((row, r) => (
            <div key={r} role="row" aria-rowindex={r + 2} className="m-row wrap" style={{ gridTemplateColumns: template, minWidth }}>
              {row.map((val, i) => (
                <div key={i} role="cell" className={'m-cell' + (nums[i] ? ' num' : '') + (val === null ? ' muted' : '')}>
                  {val === null ? 'null' : typeof val === 'number' && show[i] ? show[i](val) : String(val)}
                </div>
              ))}
            </div>
          ))}
        </div>
      ) : <div role="rowgroup" style={{ height: virt.getTotalSize(), position: 'relative' }}>
        {virt.getVirtualItems().map((v) => (
          <div key={v.index} role="row" aria-rowindex={v.index + 2} className="m-row abs"
            style={{ gridTemplateColumns: template, minWidth, height: ROW, transform: `translateY(${v.start}px)` }}>
            {result.rows[v.index].map((val, i) => (
              <div key={i} role="cell" className={'m-cell' + (nums[i] ? ' num' : '') + (val === null ? ' muted' : '')} title={typeof val === 'string' && val.length > 16 ? val : undefined}>
                {val === null ? 'null' : typeof val === 'number' && show[i] ? show[i](val) : String(val)}
              </div>
            ))}
          </div>
        ))}
      </div>}
    </div>
  )
}
