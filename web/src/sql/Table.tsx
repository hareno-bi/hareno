import { useRef } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { isNum, type Result } from './chart'

const ROW = 30

export default function Table({ result }: { result: Result }) {
  const scroller = useRef<HTMLDivElement>(null)
  const virt = useVirtualizer({ count: result.rows.length, getScrollElement: () => scroller.current, estimateSize: () => ROW, overscan: 16 })
  const nums = result.columns.map((_, i) => isNum(result, i))
  const template = `repeat(${result.columns.length}, minmax(120px, 1fr))`
  const minWidth = result.columns.length * 120
  if (result.rows.length === 0) return <div className="state empty">The query returned no rows.</div>
  return (
    <div ref={scroller} className="matrix sql-table" role="table" aria-rowcount={result.rows.length + 1} tabIndex={0}>
      <div className="m-head" role="rowgroup">
        <div className="m-row" role="row" style={{ gridTemplateColumns: template, minWidth }}>
          {result.columns.map((c, i) => <div key={i} role="columnheader" className={'m-cell' + (nums[i] ? ' num' : '')}>{c}</div>)}
        </div>
      </div>
      <div role="rowgroup" style={{ height: virt.getTotalSize(), position: 'relative' }}>
        {virt.getVirtualItems().map((v) => (
          <div key={v.index} role="row" aria-rowindex={v.index + 2} className="m-row abs"
            style={{ gridTemplateColumns: template, minWidth, height: ROW, transform: `translateY(${v.start}px)` }}>
            {result.rows[v.index].map((val, i) => (
              <div key={i} role="cell" className={'m-cell' + (nums[i] ? ' num' : '') + (val === null ? ' muted' : '')}>
                {val === null ? 'null' : String(val)}
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  )
}
