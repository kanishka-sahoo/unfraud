// Turns a card's neighbourhood (card → merchants → other cards) into what an
// analyst asks of it: which merchants does this card share, with how many
// other cards, and how many of those are confirmed fraud.
import type { components } from "./schema";

type Subgraph = components["schemas"]["Subgraph"];

export type SharedMerchant = {
  id: string;
  common: boolean; // used by too many cards to link (a super-node)
  cards: string[]; // other cards that used it
  fraud: number; // of those, with a released fraud Label
};

export type Network = {
  center: string;
  centerFraud: boolean;
  merchants: SharedMerchant[];
  cards: Map<string, { fraud: boolean; merchants: string[] }>;
  truncated: boolean;
};

export function toNetwork(g: Subgraph): Network {
  const kind = new Map(g.nodes.map((n) => [n.id, n]));
  const adj = new Map<string, Set<string>>();
  for (const e of g.edges) {
    for (const [a, b] of [[e.source, e.target], [e.target, e.source]]) {
      if (!adj.has(a)) adj.set(a, new Set());
      adj.get(a)!.add(b);
    }
  }
  const cards = new Map<string, { fraud: boolean; merchants: string[] }>();
  const merchants: SharedMerchant[] = [];
  for (const m of adj.get(g.center) ?? []) {
    const node = kind.get(m);
    if (!node || node.kind !== "payee") continue;
    const others = [...(adj.get(m) ?? [])].filter((c) => c !== g.center && kind.get(c)?.kind === "payer");
    for (const c of others) {
      const entry = cards.get(c) ?? { fraud: !!kind.get(c)?.fraud, merchants: [] };
      entry.merchants.push(m);
      cards.set(c, entry);
    }
    merchants.push({ id: m, common: !!node.super_node, cards: others, fraud: others.filter((c) => kind.get(c)?.fraud).length });
  }
  merchants.sort((a, b) => b.fraud - a.fraud || b.cards.length - a.cards.length || a.id.localeCompare(b.id));
  return { center: g.center, centerFraud: !!kind.get(g.center)?.fraud, merchants, cards, truncated: g.truncated };
}
