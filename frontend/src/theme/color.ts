/**
 * A translucent version of `color`.
 *
 * Colors in the UI are theme tokens (`var(--holo-c-red)`), so the old trick of
 * appending a hex alpha byte (`color + '22'`) no longer produces a color.
 * color-mix against transparent keeps the hue of whatever the token resolves
 * to in the active theme and only changes its opacity — for a literal color it
 * yields exactly `rgba(r, g, b, amount)`.
 */
export function tint(color: string, amount: number): string {
  const pct = Math.round(Math.min(1, Math.max(0, amount)) * 1000) / 10
  return `color-mix(in srgb, ${color} ${pct}%, transparent)`
}
