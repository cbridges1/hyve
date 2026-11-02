// The height that takes a scrolling pane (the explorer's type list, a log,
// the YAML editor) from where it is down to the bottom of the window, or
// to the top of a panel docked there (the kubectl shell,
// data-bottom-panel), so the pane scrolls and the page doesn't have to as
// well. Kept up to date as the page scrolls (a sticky pane's top moves)
// and the window resizes.
//
// Below the pane is its containers' padding (its card's, the main
// area's), which is left room for. If something follows the pane, the page
// scrolls to it anyway, and up to 64px is left. A sticky pane (the type
// list) has nothing under it once it's stuck, so it gives a fixed gap.
import { useLayoutEffect, useState, type RefObject } from 'react'

const MAX_BELOW = 64

// The padding, borders and margins under el, up to the main area's
// padding; MAX_BELOW when something visible follows it.
function spaceBelow(el: HTMLElement): number {
  const px = (v: string) => parseFloat(v) || 0
  let total = px(getComputedStyle(el).marginBottom)
  let node: HTMLElement = el
  while (node.parentElement && node.tagName !== 'MAIN') {
    for (let sib = node.nextElementSibling; sib; sib = sib.nextElementSibling) {
      if ((sib as HTMLElement).offsetHeight > 0) return MAX_BELOW
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

export function useFitHeight(ref: RefObject<HTMLElement | null>, { min = 240, gap }: { min?: number; gap?: number } = {}): number | undefined {
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
      setHeight(Math.max(min, Math.floor(bottom - rect.top - below)))
    }
    const update = () => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(measure)
    }
    measure()
    window.addEventListener('resize', update)
    window.addEventListener('scroll', update, { passive: true })
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
  }, [ref, min, gap])
  return height
}
