"use client";

import Graph from "graphology";
import forceAtlas2 from "graphology-layout-forceatlas2";
import { useRouter } from "next/navigation";
import { useEffect, useRef } from "react";
import type { components } from "@/lib/schema";

type Subgraph = components["schemas"]["Subgraph"];

const colors = { payer: "#e8e4d8", payee: "#3987e5", fraud: "#d03b3b", super: "#8c97ab" };

/** A neighbourhood drawn with Sigma.js; clicking a node re-centres on it. */
export function GraphView({ profile, data }: { profile: string; data: Subgraph }) {
  const ref = useRef<HTMLDivElement>(null);
  const router = useRouter();

  useEffect(() => {
    if (!ref.current) return;
    let renderer: { kill(): void } | null = null;
    let cancelled = false;
    const g = new Graph({ multi: false, type: "undirected" });
    data.nodes.forEach((n, i) => {
      const angle = (i / Math.max(1, data.nodes.length)) * Math.PI * 2;
      g.addNode(`${n.kind}:${n.id}`, {
        x: Math.cos(angle) * 10, y: Math.sin(angle) * 10,
        size: n.id === data.center ? 4 : n.kind === "payee" ? 2 : 2.5,
        color: n.fraud ? colors.fraud : n.super_node ? colors.super : colors[n.kind],
        label: n.id === data.center || n.fraud ? n.id : undefined,
        node: n.id, kind: n.kind,
      });
    });
    const key = (id: string) => data.nodes.find((n) => n.id === id);
    for (const e of data.edges) {
      const a = key(e.source), b = key(e.target);
      if (!a || !b) continue;
      const ka = `${a.kind}:${a.id}`, kb = `${b.kind}:${b.id}`;
      if (ka !== kb && !g.hasEdge(ka, kb)) g.addEdge(ka, kb, { color: "#2c3b57", size: 1 });
    }
    if (g.order > 1) forceAtlas2.assign(g, { iterations: 200, settings: forceAtlas2.inferSettings(g) });

    import("sigma").then(({ default: Sigma }) => {
      if (cancelled || !ref.current) return;
      // A styles declaration replaces sigma's defaults, so every binding is listed.
      const s = new Sigma(g, ref.current, {
        styles: {
          nodes: {
            x: { attribute: "x" }, y: { attribute: "y" },
            size: { attribute: "size", defaultValue: 4 },
            color: { attribute: "color", defaultValue: "#e8e4d8" },
            label: { attribute: "label" },
            labelColor: "#e8e4d8",
          },
          edges: { size: { attribute: "size", defaultValue: 1 }, color: { attribute: "color", defaultValue: "#2c3b57" } },
        },
      });
      s.on("clickNode", ({ node }) => {
        const attrs = g.getNodeAttributes(node);
        if (attrs.kind === "payer") router.push(`/${profile}/graph/${encodeURIComponent(attrs.node)}`);
      });
      renderer = s;
    });
    return () => {
      cancelled = true;
      renderer?.kill();
    };
  }, [data, profile, router]);

  return <div ref={ref} className="graph-canvas" role="img" aria-label={`Graph around ${data.center}: ${data.nodes.length} nodes, ${data.edges.length} links`} />;
}
