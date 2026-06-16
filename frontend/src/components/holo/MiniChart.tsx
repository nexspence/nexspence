import { useEffect, useRef, useState } from 'react';
import type { RefObject } from 'react';

export interface MiniChartPoint {
  label: string;
  value: number;
}

interface MiniChartProps {
  data: MiniChartPoint[];
  type: 'line' | 'area';
  /** 6-digit hex (#rrggbb); area fill appends "1a" alpha. */
  color: string;
  height?: number;
  valueFormatter?: (v: number) => string;
  ariaLabel: string;
}

const PAD = { top: 10, right: 12, bottom: 22, left: 46 };
const TICK_COLOR = '#64748b';
const GRID_COLOR = 'rgba(255,255,255,0.05)';

function useContainerWidth(): [RefObject<HTMLDivElement | null>, number] {
  const ref = useRef<HTMLDivElement | null>(null);
  const [width, setWidth] = useState(600);
  useEffect(() => {
    const el = ref.current;
    // jsdom has no ResizeObserver — fall back to the default width.
    if (!el || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(entries => {
      const w = entries[0]?.contentRect.width;
      if (w) setWidth(w);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width];
}

const defaultFormat = (v: number): string => {
  if (Math.abs(v) >= 1_000_000_000) return `${(v / 1_000_000_000).toFixed(1)}G`;
  if (Math.abs(v) >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (Math.abs(v) >= 1_000) return `${(v / 1_000).toFixed(1)}k`;
  return Number.isInteger(v) ? String(v) : v.toFixed(2);
};

/* ---------- MiniChart: zero-dependency SVG line/area chart ---------- */
export function MiniChart({ data, type, color, height = 200, valueFormatter = defaultFormat, ariaLabel }: MiniChartProps) {
  const [wrapRef, width] = useContainerWidth();
  const [hover, setHover] = useState<number | null>(null);

  if (data.length === 0) return null;

  const innerW = Math.max(10, width - PAD.left - PAD.right);
  const innerH = height - PAD.top - PAD.bottom;

  let min = Math.min(...data.map(d => d.value));
  let max = Math.max(...data.map(d => d.value));
  if (min === max) {
    // Flat series: pad the range so the line sits mid-chart.
    min -= 1;
    max += 1;
  }

  const x = (i: number) => PAD.left + (data.length === 1 ? innerW / 2 : (i / (data.length - 1)) * innerW);
  const y = (v: number) => PAD.top + innerH - ((v - min) / (max - min)) * innerH;

  const linePath = data.map((d, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)},${y(d.value).toFixed(1)}`).join(' ');
  const baseY = (PAD.top + innerH).toFixed(1);
  const areaPath = `${linePath} L${x(data.length - 1).toFixed(1)},${baseY} L${x(0).toFixed(1)},${baseY} Z`;

  const yTicks = [0, 1, 2, 3].map(i => min + ((max - min) * i) / 3);
  const xTickStep = Math.max(1, Math.ceil(data.length / 6));

  const onMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const frac = (e.clientX - rect.left - PAD.left) / innerW;
    const idx = Math.round(frac * (data.length - 1));
    setHover(Math.min(data.length - 1, Math.max(0, idx)));
  };

  return (
    <div ref={wrapRef} role="img" aria-label={ariaLabel} style={{ position: 'relative', width: '100%' }}>
      <svg width={width} height={height} onMouseMove={onMove} onMouseLeave={() => setHover(null)}>
        {yTicks.map(v => (
          <g key={v}>
            <line x1={PAD.left} x2={width - PAD.right} y1={y(v)} y2={y(v)} stroke={GRID_COLOR} />
            <text x={PAD.left - 6} y={y(v) + 3} textAnchor="end" fontSize={10} fill={TICK_COLOR}>
              {valueFormatter(v)}
            </text>
          </g>
        ))}
        {data.map((d, i) =>
          i % xTickStep === 0 ? (
            <text key={i} x={x(i)} y={height - 6} textAnchor="middle" fontSize={10} fill={TICK_COLOR}>
              {d.label}
            </text>
          ) : null,
        )}
        {data.length === 1 ? (
          <circle cx={x(0)} cy={y(data[0].value)} r={3} fill={color} />
        ) : (
          <path
            d={type === 'area' ? areaPath : linePath}
            fill={type === 'area' ? `${color}1a` : 'none'}
            stroke={color}
            strokeWidth={2}
          />
        )}
        {hover != null && data[hover] && (
          <circle cx={x(hover)} cy={y(data[hover].value)} r={3.5} fill={color} stroke="#0d1526" strokeWidth={1.5} />
        )}
      </svg>
      {hover != null && data[hover] && (
        <div
          style={{
            position: 'absolute',
            left: Math.min(x(hover) + 8, width - 130),
            top: 8,
            background: '#0d1526',
            border: '1px solid rgba(255,255,255,0.1)',
            borderRadius: 8,
            padding: '6px 10px',
            fontSize: 12,
            color: '#dbeafe',
            pointerEvents: 'none',
            whiteSpace: 'nowrap',
          }}
        >
          <div style={{ color: TICK_COLOR }}>{data[hover].label}</div>
          <div style={{ fontWeight: 600 }}>{valueFormatter(data[hover].value)}</div>
        </div>
      )}
    </div>
  );
}
