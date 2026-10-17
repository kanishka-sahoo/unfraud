"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { networkHref } from "@/lib/format";

/** Jump to any card's network by its id (user-card index, such as 856-1). */
export function CardSearch({ initial = "" }: { initial?: string }) {
  const router = useRouter();
  const [card, setCard] = useState(initial);
  return (
    <form className="search" onSubmit={(e) => { e.preventDefault(); if (card.trim()) router.push(networkHref(card.trim())); }}>
      <input aria-label="Card id" placeholder="Card id, such as 856-1" value={card} onChange={(e) => setCard(e.target.value)} className="mono" />
      <button type="submit">Show network</button>
    </form>
  );
}
