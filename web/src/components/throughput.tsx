"use client";

import { useState } from "react";

type Point = { minute: string; provisional: number; revised: number; flagged: number };

const W = 1100, H = 240, L = 56, R = 90, T = 12, B = 26;

/** Decisions per minute by Stage over the last hour (one shared axis). */
export function Throughput({ data }: { data: Point[] }) {
  const [hover, setHover] = useState<number | null>(null);
  if (data.length === 0) {
    return <p className="empty">No Decisions in the last hour. Start a replay to see throughput.</p>;
  }
  const max = Math.max(1, ...data.flatMap((d) => [d.provisional, d.revised]));
  const step = niceStep(max);
  const top = Math.ceil(max / step) * step;
  const x = (i: number) => L + (data.length === 1 ? (W - L - R) / 2 : (i / (data.length - 1)) * (W - L - R));
  const y = (v: number) => T + (1 - v / top) * (H - T - B);
  const line = (key: "provisional" | "revised") => data.map((d, i) => `${i ? "L" : "M"}${x(i)},${y(d[key])}`).join(" ");
  const ticks = Array.from({ length: Math.round(top / step) + 1 }, (_, i) => i * step);
  const last = data.length - 1;
  const fmtMin = (iso: string) => new Date(iso).toISOString().slice(11, 16);

  function onMove(e: React.PointerEvent<SVGRectElement>) {
    const box = e.currentTarget.getBoundingClientRect();
    const px = ((e.clientX - box.left) / box.width) * (W - L - R);
    setHover(Math.max(0, Math.min(last, Math.round((px / (W - L - R)) * last))));
  }

  const h = hover === null ? null : data[hover];
  return (
    <div className="chart">
      <div className="legend">
        <span><span className="key" style={{ background: "var(--series-1)" }} />On arrival</span>
        <span><span className="key" style={{ background: "var(--series-2)" }} />Graph revision</span>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Decisions per minute over the last hour, on arrival and after graph revision">
        <g className="grid">
          {ticks.map((t) => <line key={t} x1={L} x2={W - R} y1={y(t)} y2={y(t)} />)}
        </g>
        <g className="axis">
          {ticks.map((t) => <text key={t} x={L - 8} y={y(t) + 4} textAnchor="end">{t.toLocaleString("en-US")}</text>)}
          <text x={L} y={H - 6}>{fmtMin(data[0].minute)}</text>
          <text x={W - R} y={H - 6} textAnchor="end">{fmtMin(data[last].minute)} UTC</text>
        </g>
        <path d={line("provisional")} fill="none" stroke="var(--series-1)" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
        <path d={line("revised")} fill="none" stroke="var(--series-2)" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
        {(["provisional", "revised"] as const).map((k, i) => (
          <g key={k}>
            <circle cx={x(last)} cy={y(data[last][k])} r="4" fill={`var(--series-${i + 1})`} stroke="var(--ink)" strokeWidth="2" />
            <text x={x(last) + 8} y={y(data[last][k]) + 4 + (i === 1 && Math.abs(y(data[last].provisional) - y(data[last].revised)) < 12 ? 12 : 0)} fill="var(--paper-2)" fontSize="11">
              {data[last][k].toLocaleString("en-US")}/min
            </text>
          </g>
        ))}
        {h && hover !== null && (
          <g>
            <line x1={x(hover)} x2={x(hover)} y1={T} y2={H - B} stroke="var(--paper-2)" strokeWidth="1" />
            <circle cx={x(hover)} cy={y(h.provisional)} r="4" fill="var(--series-1)" stroke="var(--ink)" strokeWidth="2" />
            <circle cx={x(hover)} cy={y(h.revised)} r="4" fill="var(--series-2)" stroke="var(--ink)" strokeWidth="2" />
          </g>
        )}
        <rect x={L} y={T} width={W - L - R} height={H - T - B} fill="transparent" onPointerMove={onMove} onPointerLeave={() => setHover(null)} />
      </svg>
      {h && hover !== null && (
        <div className="tip" style={{ left: `${(x(hover) / W) * 100}%`, top: `${(y(Math.max(h.provisional, h.revised)) / H) * 100}%` }}>
          {fmtMin(h.minute)} UTC · on arrival {h.provisional.toLocaleString("en-US")} · revised {h.revised.toLocaleString("en-US")} · flagged {h.flagged.toLocaleString("en-US")}
        </div>
      )}
    </div>
  );
}

function niceStep(max: number): number {
  const raw = max / 4;
  const mag = 10 ** Math.floor(Math.log10(raw));
  return [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? raw;
}
