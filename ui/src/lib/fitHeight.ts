// The height that takes a scrolling pane (the explorer's type list, a log,
// the YAML editor) from where it is down to the bottom of the window, or
// to the top of a panel docked there (the kubectl shell,
// data-bottom-panel), so the pane scrolls and the page doesn't have to as
// well. Measured from where the pane sits with the page scrolled to its
// top, so scrolling doesn't change it (it would grow as its top moved up,
// lengthening the page as it went); a sticky pane (sticky: true), whose
// top does move as the page scrolls, follows the scroll. Kept up to date
// as the window resizes and the content above changes.
//
// Below the pane is whatever follows it in its containers (Save and
// Cancel, a pager) and their padding, down to the main area's: room is
// left for up to 160px of it, so those stay in view; past that the page
// scrolls to the rest. A sticky pane (the type list) has nothing under it
// once it's stuck, so it gives a fixed gap.
import { useLayoutEffect, useState, type RefObject } from 'react'

const MAX_BELOW = 160

// The height of what's under el, up to the main area's padding: its
// containers' padding, borders and margins, and the elements after it.
function spaceBelow(el: HTMLElement): number {
  const px = (v: string) => parseFloat(v) || 0
  let total = px(getComputedStyle(el).marginBottom)
  let node: HTMLElement = el
  while (node.parentElement && node.tagName !== 'MAIN') {
    for (let sib = node.nextElementSibling as HTMLElement | null; sib; sib = sib.nextElementSibling as HTMLElement | null) {
      if (!sib.offsetHeight || getComputedStyle(sib).position === 'fixed') continue
      const st = getComputedStyle(sib)
      total += sib.offsetHeight + px(st.marginTop) + px(st.marginBottom)
    }
    const p = node.parentElement
    const s = getComputedStyle(p)
    total += px(s.paddingBottom) + px(s.borderBottomWidth)
    if (p.tagName !== 'MAIN') total += px(s.marginBottom)
    node = p
  }
  // Outside the main area (a dialog): a little room.
  if (node.tagName !== 'MAIN') total += 16
  return Math.min(MAX_BELOW, total)
}

export function useFitHeight(
  ref: RefObject<HTMLElement | null>,
  { min = 240, gap, sticky = false }: { min?: number; gap?: number; sticky?: boolean } = {},
): number | undefined {
  const [height, setHeight] = useState<number>()
  useLayoutEffect(() => {
    let frame = 0
    const measure = () => {
      const el = ref.current
      if (!el) return
      const rect = el.getBoundingClientRect()
      const panel = document.querySelector('[data-bottom-panel]')
      const bottom = panel ? panel.getBoundingClientRect().top : window.innerHeight
      const below = gap ?? spaceBelow(el)
      const top = sticky ? rect.top : rect.top + window.scrollY
      // A pixel spare: positions are fractional, and rounding up a half
      // pixel would make the page scroll by one.
      setHeight(Math.max(min, Math.floor(bottom - top - below) - 1))
    }
    const update = () => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(measure)
    }
    measure()
    window.addEventListener('resize', update)
    if (sticky) window.addEventListener('scroll', update, { passive: true })
    // Content above the pane changing size (a banner, the shell opening)
    // moves it without a scroll or a resize.
    const ro = new ResizeObserver(update)
    ro.observe(document.body)
    return () => {
      cancelAnimationFrame(frame)
      window.removeEventListener('resize', update)
      window.removeEventListener('scroll', update)
      ro.disconnect()
    }
  }, [ref, min, gap, sticky])
  return height
}
