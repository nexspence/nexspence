import { useLayoutEffect, useRef, useState } from 'react'

/**
 * useOverflowTitle reveals text that CSS has clipped, and only then.
 *
 * The UI truncates with `text-overflow: ellipsis` in a dozen places, and in
 * almost all of them the clipped part was simply unrecoverable. The obvious fix
 * — a `title` on everything that might overflow — is worse than it looks: the
 * tooltip then fires on rows that are perfectly readable, turning a list into a
 * field of popups. So the title exists only while `scrollWidth > clientWidth`,
 * which is the browser's own answer to "is there text you cannot see".
 *
 * Attach `ref` to the element that does the clipping — the one carrying
 * `overflow: hidden` — not to a wrapper, or the comparison measures the wrong
 * box.
 *
 * Accessibility note: this is for sighted mouse users. Screen readers already
 * read the full string, because ellipsis is a visual effect and the text is
 * intact in the DOM. Sighted keyboard-only users are the group still left out —
 * `title` does not open on focus — and the fix for that is not `tabIndex` on
 * every table cell, which would bury real controls in tab order. It needs a
 * real tooltip component, which is a separate decision.
 */
export function useOverflowTitle<T extends HTMLElement>(text: string | null | undefined) {
  const ref = useRef<T>(null)
  const [overflowing, setOverflowing] = useState(false)

  // Layout effect, not effect: measuring after paint would show one frame with
  // the wrong answer on every mount.
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return

    const measure = () => setOverflowing(el.scrollWidth > el.clientWidth)
    measure()

    // Absent in jsdom, and in any environment old enough to lack it. The mount
    // measurement above still stands; only the response to resizing is lost.
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [text])

  return { ref, title: overflowing && text ? text : undefined }
}
