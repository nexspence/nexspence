import type { CSSProperties, HTMLAttributes, ReactNode, Ref } from 'react'
import { useOverflowTitle } from './useOverflowTitle'

/** The elements that clip text in this UI. */
type TruncatedTag = 'div' | 'span' | 'td'

interface TruncatedProps extends Omit<HTMLAttributes<HTMLElement>, 'title' | 'children'> {
  /** The value to clip, and to reveal on hover when it does not fit. */
  text: string
  /** Rendered instead of `text` when the value needs decoration. `text` still supplies the title. */
  children?: ReactNode
  as?: TruncatedTag
  style?: CSSProperties
}

const clip: CSSProperties = {
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
}

/**
 * Truncated clips a value to its container and reveals the whole of it on hover
 * — but only when it is actually cut off. See useOverflowTitle for why the
 * "only when" matters.
 *
 * It owns the three clipping properties rather than leaving them to the call
 * site. They were being copied by hand at every site, and a site that copies
 * two of the three does not clip at all — it just overflows.
 *
 * A component rather than a bare hook because nearly every clipped value here
 * is inside a `.map()`, and a hook cannot be called per row.
 */
export function Truncated({ text, children, as: Tag = 'div', style, ...rest }: TruncatedProps) {
  const { ref, title } = useOverflowTitle<HTMLElement>(text)

  return (
    <Tag
      // One cast, here: the tag is chosen at the call site, so no single
      // element type describes it. The hook only ever reads scrollWidth and
      // clientWidth, which every element has.
      ref={ref as Ref<HTMLDivElement & HTMLSpanElement & HTMLTableCellElement>}
      title={title}
      style={{ ...clip, ...style }}
      {...rest}
    >
      {children ?? text}
    </Tag>
  )
}
