"use client";

import { useState } from "react";

export type Series = { name: string; color: string; points: { x: number; y: number }[] };

const W = 1100, H = 260, L = 44, R = 150, T = 12, B = 30;

/** Several series on one y axis (never two), with a crosshair tooltip. */
export function LineChart({ series, xLabel, yMax = 1, yFormat = (v) => v.toFixed(2), label }: {
  series: Series[]; xLabel: string; yMax?: number; yFormat?: (v: number) => string; label: string;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const xs = [...new Set(series.flatMap((s) => s.points.map((p) => p.x)))].sort((a, b) => a - b);
  if (xs.length === 0) return null;
  const x = (v: number) => L + ((v - xs[0]) / Math.max(1, xs[xs.length - 1] - xs[0])) * (W - L - R);
  const y = (v: number) => T + (1 - v / yMax) * (H - T - B);
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((f) => f * yMax);
  const at = (s: Series, v: number) => s.points.find((p) => p.x === v);

  function onMove(e: React.PointerEvent<SVGRectElement>) {
    const box = e.currentTarget.getBoundingClientRect();
    const v = xs[0] + ((e.clientX - box.left) / box.width) * (xs[xs.length - 1] - xs[0]);
    setHover(xs.reduce((best, c) => (Math.abs(c - v) < Math.abs(best - v) ? c : best), xs[0]));
  }
  const ends = series.map((s) => ({ s, p: s.points[s.points.length - 1] })).sort((a, b) => y(a.p.y) - y(b.p.y));
  // End labels are pushed apart so they never overlap.
  const labelY: { s: Series; p: { x: number; y: number }; y: number }[] = [];
  for (const { s, p } of ends) {
    const prev = labelY.length ? labelY[labelY.length - 1].y : -Infinity;
    labelY.push({ s, p, y: Math.max(y(p.y) + 4, prev + 13) });
  }

  return (
    <div className="chart">
      <div className="legend">
        {series.map((s) => <span key={s.name}><span className="key" style={{ background: s.color }} />{s.name}</span>)}
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={label}>
        <g className="grid">{ticks.map((t) => <line key={t} x1={L} x2={W - R} y1={y(t)} y2={y(t)} />)}</g>
        <g className="axis">
          {ticks.map((t) => <text key={t} x={L - 8} y={y(t) + 4} textAnchor="end">{yFormat(t)}</text>)}
          {xs.map((v) => <text key={v} x={x(v)} y={H - 10} textAnchor="middle">{v}</text>)}
          <text x={W - R} y={H} textAnchor="end">{xLabel}</text>
        </g>
        {series.map((s) => (
          <path key={s.name} d={s.points.map((p, i) => `${i ? "L" : "M"}${x(p.x)},${y(p.y)}`).join(" ")}
            fill="none" stroke={s.color} strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
        ))}
        {labelY.map(({ s, p, y: ly }) => (
          <g key={s.name}>
            <circle cx={x(p.x)} cy={y(p.y)} r="4" fill={s.color} stroke="var(--panel)" strokeWidth="2" />
            <text x={x(p.x) + 8} y={ly} fill="var(--paper-2)" fontSize="11">{s.name}</text>
          </g>
        ))}
        {hover !== null && (
          <g>
            <line x1={x(hover)} x2={x(hover)} y1={T} y2={H - B} stroke="var(--paper-2)" strokeWidth="1" />
            {series.map((s) => {
              const p = at(s, hover);
              return p ? <circle key={s.name} cx={x(p.x)} cy={y(p.y)} r="4" fill={s.color} stroke="var(--panel)" strokeWidth="2" /> : null;
            })}
          </g>
        )}
        <rect x={L} y={T} width={W - L - R} height={H - T - B} fill="transparent" onPointerMove={onMove} onPointerLeave={() => setHover(null)} />
      </svg>
      {hover !== null && (
        <div className="tip" style={{ left: `${(x(hover) / W) * 100}%`, top: "12%" }}>
          {xLabel} {hover}: {series.map((s) => `${s.name} ${at(s, hover) ? yFormat(at(s, hover)!.y) : "–"}`).join(" · ")}
        </div>
      )}
    </div>
  );
}
