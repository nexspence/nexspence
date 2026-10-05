// Where the menu goes: below the trigger (top) or above it (bottom), and how
// tall it may get without running off the viewport.
export interface DropPos { top?: number; bottom?: number; left: number; width: number; maxHeight: number }

const MENU_MAX_HEIGHT = 280
const MENU_GAP = 4
const VIEWPORT_MARGIN = 8

// menuPlacement opens the menu below the trigger, or above it when the space
// below is too small for a full menu and the space above is larger — near the
// bottom of the screen a downward menu ran off the viewport.
export function menuPlacement(r: DOMRect, viewportHeight: number): DropPos {
  const below = viewportHeight - r.bottom - MENU_GAP - VIEWPORT_MARGIN
  const above = r.top - MENU_GAP - VIEWPORT_MARGIN
  if (below < MENU_MAX_HEIGHT && above > below) {
    return { bottom: viewportHeight - r.top + MENU_GAP, left: r.left, width: r.width, maxHeight: Math.min(MENU_MAX_HEIGHT, above) }
  }
  return { top: r.bottom + MENU_GAP, left: r.left, width: r.width, maxHeight: Math.min(MENU_MAX_HEIGHT, Math.max(below, 120)) }
}
