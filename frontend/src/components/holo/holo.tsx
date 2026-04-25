import React, { useRef, useState, useEffect } from 'react';

/**
 * Holographic Depth — React component kit for self_nexus.
 *
 *   import './components/holo/holo.css';
 *   import { HoloApp, HoloCard, TiltCard, HoloButton, HoloText, HoloPill,
 *            HoloInput, HoloTabs, CountUp } from './components/holo';
 */

type Children = { children?: React.ReactNode };

/* ---------- App shell (background + scanlines) ---------- */
export function HoloApp({ children, className = '', ...rest }: Children & React.HTMLAttributes<HTMLDivElement>) {
  return <div className={`holo-app ${className}`} {...rest}>{children}</div>;
}

/* ---------- Holographic gradient text ---------- */
export function HoloText({ children, as: Tag = 'span', className = '', ...rest }: Children & { as?: any; className?: string }) {
  return <Tag className={`holo-text ${className}`} {...rest}>{children}</Tag>;
}

/* ---------- Glass card ---------- */
export interface HoloCardProps extends React.HTMLAttributes<HTMLDivElement> {
  accent?: boolean;
  edge?: boolean;
}
export function HoloCard({ accent, edge, className = '', children, ...rest }: HoloCardProps) {
  const cls = ['holo-card', accent && 'holo-card--accent', className].filter(Boolean).join(' ');
  return (
    <div className={cls} {...rest}>
      {edge && <div className="holo-card__edge" />}
      {children}
    </div>
  );
}

/* ---------- 3D tilt wrapper ----------
 * Wrap any card in <TiltCard> to make it tilt + shine on hover.
 * Children should NOT have their own perspective transform.
 */
export interface TiltCardProps extends React.HTMLAttributes<HTMLDivElement> {
  intensity?: number; // degrees
}
export function TiltCard({ intensity = 10, style, className = '', children, ...rest }: TiltCardProps) {
  const ref = useRef<HTMLDivElement>(null);
  const [t, setT] = useState({ rx: 0, ry: 0, mx: 50, my: 50 });

  function onMove(e: React.MouseEvent<HTMLDivElement>) {
    const r = ref.current!.getBoundingClientRect();
    const x = (e.clientX - r.left) / r.width;
    const y = (e.clientY - r.top) / r.height;
    setT({ rx: (0.5 - y) * intensity, ry: (x - 0.5) * intensity, mx: x * 100, my: y * 100 });
  }
  function onLeave() { setT({ rx: 0, ry: 0, mx: 50, my: 50 }); }

  return (
    <div
      ref={ref}
      onMouseMove={onMove}
      onMouseLeave={onLeave}
      className={className}
      style={{
        position: 'relative',
        transformStyle: 'preserve-3d',
        transform: `perspective(1000px) rotateX(${t.rx}deg) rotateY(${t.ry}deg)`,
        transition: 'transform 0.15s ease-out',
        ...style,
      }}
      {...rest}
    >
      <div style={{
        position: 'absolute', inset: 0, borderRadius: 'inherit', pointerEvents: 'none', zIndex: 2,
        background: `radial-gradient(circle at ${t.mx}% ${t.my}%, rgba(255,255,255,0.12), transparent 50%)`,
      }} />
      {children}
    </div>
  );
}

/* ---------- Buttons ---------- */
type BtnVariant = 'default' | 'primary' | 'danger';
export interface HoloButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: BtnVariant;
  icon?: React.ReactNode;
}
export function HoloButton({ variant = 'default', icon, children, className = '', type = 'button', ...rest }: HoloButtonProps) {
  const cls = ['holo-btn', variant !== 'default' && `holo-btn--${variant}`, className].filter(Boolean).join(' ');
  return <button type={type} className={cls} {...rest}>{icon}{children}</button>;
}

/* ---------- Pills ---------- */
type PillTone = 'default' | 'success' | 'warn' | 'danger';
export function HoloPill({ tone = 'default', children, className = '', ...rest }: { tone?: PillTone } & React.HTMLAttributes<HTMLSpanElement>) {
  const cls = ['holo-pill', tone !== 'default' && `holo-pill--${tone}`, className].filter(Boolean).join(' ');
  return <span className={cls} {...rest}>{children}</span>;
}

/* ---------- Input ---------- */
export const HoloInput = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className = '', ...rest }, ref) => (
    <input ref={ref} className={`holo-input ${className}`} {...rest} />
  )
);

/* ---------- Tabs ---------- */
export interface HoloTabItem { value: string; label: React.ReactNode }
export function HoloTabs({ items, value, onChange }: { items: HoloTabItem[]; value: string; onChange: (v: string) => void }) {
  return (
    <div className="holo-tabs">
      {items.map(t => (
        <button key={t.value} className={`holo-tab ${value === t.value ? 'active' : ''}`} onClick={() => onChange(t.value)}>
          {t.label}
        </button>
      ))}
    </div>
  );
}

/* ---------- CountUp number ---------- */
export function CountUp({ to, suffix = '', dur = 1200, decimals }: { to: number; suffix?: string; dur?: number; decimals?: number }) {
  const [v, setV] = useState(0);
  useEffect(() => {
    const start = performance.now();
    let raf: number;
    const tick = (t: number) => {
      const p = Math.min(1, (t - start) / dur);
      const eased = 1 - Math.pow(1 - p, 3);
      setV(to * eased);
      if (p < 1) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [to, dur]);
  const d = decimals ?? (Number.isInteger(to) ? 0 : 1);
  return <>{v.toLocaleString(undefined, { minimumFractionDigits: d, maximumFractionDigits: d })}{suffix}</>;
}

/* ---------- Modal ---------- */
export function HoloModal({ open, onClose, children, style }: { open: boolean; onClose: () => void; children: React.ReactNode; style?: React.CSSProperties }) {
  if (!open) return null;
  return (
    <div className="holo-overlay" onClick={onClose}>
      <div className="holo-modal" style={style} onClick={e => e.stopPropagation()}>{children}</div>
    </div>
  );
}
