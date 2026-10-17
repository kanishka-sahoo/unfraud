"use client";

import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";
import { merchantShort, networkHref } from "@/lib/format";
import { toNetwork } from "@/lib/network";
import type { components } from "@/lib/schema";

const S = 760, C = S / 2, R1 = 150, R2 = 300;

type Pos = { x: number; y: number };
type Tip = { x: number; y: number; title: string; lines: string[] } | null;

/**
 * A radial map of one card's neighbourhood: the card in the middle, the
 * merchants it used on the inner ring, and the other cards that used them on
 * the outer ring, each next to the first merchant it shares. Confirmed fraud
 * is red; hovering a merchant highlights its cards.
 */
export function NetworkView({ graph }: { graph: components["schemas"]["Subgraph"] }) {
  const router = useRouter();
  const [focus, setFocus] = useState<string | null>(null);
  const [tip, setTip] = useState<Tip>(null);
  const net = useMemo(() => toNetwork(graph), [graph]);
  const cards = net.cards;

  const layout = useMemo(() => {
    const ms = net.merchants;
    // Each merchant gets an arc in proportion to the cards it places (at
    // least one slot), so busy merchants spread out and quiet ones stay small.
    const placed = new Map<string, string[]>();
    const seen = new Set<string>();
    for (const m of ms) {
      const own = m.cards.filter((c) => !seen.has(c));
      own.forEach((c) => seen.add(c));
      placed.set(m.id, own);
    }
    const weight = (id: string) => Math.max(1, Math.sqrt(placed.get(id)!.length));
    const total = ms.reduce((s, m) => s + weight(m.id), 0) || 1;
    const merchantPos = new Map<string, Pos & { a0: number; a1: number }>();
    const cardPos = new Map<string, Pos>();
    let a = -Math.PI / 2;
    for (const m of ms) {
      const span = (weight(m.id) / total) * Math.PI * 2;
      const mid = a + span / 2;
      merchantPos.set(m.id, { x: C + R1 * Math.cos(mid), y: C + R1 * Math.sin(mid), a0: a, a1: a + span });
      const own = placed.get(m.id)!;
      // Cards fan out over the merchant's arc, in rows if the arc is crowded.
      const perRow = Math.max(1, Math.floor((span * R2) / 11));
      own.forEach((c, i) => {
        const row = Math.floor(i / perRow), col = i % perRow;
        const inRow = Math.min(perRow, own.length - row * perRow);
        const t = inRow === 1 ? 0.5 : (col + 0.5) / inRow;
        const ang = a + span * (0.08 + 0.84 * t);
        const r = R2 - 18 + row * 12;
        cardPos.set(c, { x: C + r * Math.cos(ang), y: C + r * Math.sin(ang) });
      });
      a += span;
    }
    return { merchantPos, cardPos };
  }, [net.merchants]);

  // What stays lit while a merchant or card is hovered: it and its links.
  const active = useMemo(() => {
    if (!focus) return null;
    const m = net.merchants.find((x) => x.id === focus);
    return new Set([focus, ...(m ? m.cards : cards.get(focus)?.merchants ?? [])]);
  }, [focus, net.merchants, cards]);
  const lit = (...ids: string[]) => !active || ids.every((id) => active.has(id));

  const show = (e: React.MouseEvent, title: string, lines: string[]) => {
    const box = (e.currentTarget as SVGGraphicsElement).ownerSVGElement!.getBoundingClientRect();
    setTip({ x: e.clientX - box.left, y: e.clientY - box.top, title, lines });
  };

  return (
    <div className="net" onMouseLeave={() => { setFocus(null); setTip(null); }}>
      <svg viewBox={`0 0 ${S} ${S}`} role="img" aria-label={`Card ${net.center}: ${net.merchants.length} merchants, ${cards.size} other cards`}>
        <circle cx={C} cy={C} r={R1} fill="none" stroke="var(--line)" strokeDasharray="2 6" />
        <circle cx={C} cy={C} r={R2 - 18} fill="none" stroke="var(--line)" strokeDasharray="2 6" />
        {/* card → merchant edges, then merchant → card */}
        {net.merchants.map((m) => {
          const p = layout.merchantPos.get(m.id)!;
          return <line key={`c-${m.id}`} x1={C} y1={C} x2={p.x} y2={p.y} className={`edge ${m.fraud ? "hot" : ""} ${focus === m.id ? "focus" : ""} ${lit(m.id) ? "" : "faded"}`} />;
        })}
        {[...cards].flatMap(([c, info]) => info.merchants.map((m) => {
          const p = layout.merchantPos.get(m)!, q = layout.cardPos.get(c)!;
          return <line key={`${m}-${c}`} x1={p.x} y1={p.y} x2={q.x} y2={q.y} className={`edge ${info.fraud ? "hot" : ""} ${lit(m, c) ? "" : "faded"}`} strokeOpacity={0.55} />;
        }))}
        {[...cards].map(([c, info]) => {
          const q = layout.cardPos.get(c)!;
          return (
            <g key={c} className={`n ${lit(c) ? "" : "faded"}`}
              onMouseEnter={(e) => { setFocus(c); show(e, `Card ${c}`, [info.fraud ? "Confirmed fraud (released label)" : "No confirmed fraud", `Shares ${info.merchants.length} merchant${info.merchants.length > 1 ? "s" : ""} with this card`, "Click to centre on it"]); }}
              onClick={() => router.push(networkHref(c))}>
              <circle cx={q.x} cy={q.y} r={info.fraud ? 5.5 : 4} fill={info.fraud ? "var(--high)" : "var(--text-2)"} stroke="var(--surface)" strokeWidth="1.5" />
            </g>
          );
        })}
        {net.merchants.map((m) => {
          const p = layout.merchantPos.get(m.id)!;
          const s = m.common ? 9 : 11;
          return (
            <g key={m.id} className={`n ${lit(m.id) ? "" : "faded"}`}
              onMouseEnter={(e) => { setFocus(m.id); show(e, merchantShort(m.id), m.common ? ["Used by more than 100 cards in 30 days", "Too common to link cards through"] : [`${m.cards.length} other card${m.cards.length === 1 ? "" : "s"} used it`, `${m.fraud} with confirmed fraud`]); }}>
              <rect x={p.x - s / 2} y={p.y - s / 2} width={s} height={s} rx="2.5" transform={`rotate(45 ${p.x} ${p.y})`}
                fill={m.common ? "var(--surface)" : m.fraud ? "var(--high)" : "var(--lowers)"} stroke={m.common ? "var(--muted)" : "var(--surface)"} strokeWidth="1.5" />
            </g>
          );
        })}
        <g>
          <circle cx={C} cy={C} r="16" fill="var(--accent)" fillOpacity="0.18" />
          <circle cx={C} cy={C} r="9" fill={net.centerFraud ? "var(--high)" : "var(--accent)"} stroke="var(--surface)" strokeWidth="2" />
          <text x={C} y={C + 32} textAnchor="middle" style={{ fill: "var(--text)", fontWeight: 600, fontSize: 13 }}>Card {net.center}</text>
        </g>
      </svg>
      {tip && (
        <div className="tip" style={{ left: tip.x, top: tip.y }}>
          <b>{tip.title}</b>
          {tip.lines.map((l) => <div key={l} className="muted">{l}</div>)}
        </div>
      )}
    </div>
  );
}
