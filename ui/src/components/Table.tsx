// A table that turns into a list of cards on a phone (below md; the
// .table-cards rules in index.css): each row a card, each cell labelled
// with its column's heading. The labels are the cells' data-label, filled
// in here from the head as rows come and go, so a table needn't repeat
// them. A row's first cell is its card's title, and an unheaded column
// (a row's actions) sits along its foot.
import { useLayoutEffect, useRef, type ReactNode } from 'react'

export function Table({ className = 'table', children }: { className?: string; children: ReactNode }) {
  const ref = useRef<HTMLTableElement>(null)
  useLayoutEffect(() => {
    const table = ref.current
    if (!table) return
    const label = () => {
      const heads = Array.from(table.querySelectorAll(':scope > thead > tr > th'), (th) => th.textContent?.trim() || '')
      table.querySelectorAll(':scope > tbody > tr').forEach((tr) => {
        let col = 0
        for (const cell of Array.from(tr.children) as HTMLTableCellElement[]) {
          // A cell across the row (an empty or loading row's) has none.
          const l = cell.colSpan > 1 ? '' : heads[col] || ''
          if (cell.getAttribute('data-label') !== l) cell.setAttribute('data-label', l)
          col += cell.colSpan || 1
        }
      })
    }
    label()
    const watch = new MutationObserver(label)
    watch.observe(table, { childList: true, subtree: true })
    return () => watch.disconnect()
  }, [])
  return (
    <table ref={ref} className={`${className} table-cards`}>
      {children}
    </table>
  )
}
