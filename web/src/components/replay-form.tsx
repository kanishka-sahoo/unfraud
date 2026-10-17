"use client";

import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";
import { PROFILE } from "@/lib/format";


/** Starts a replay Job: a dataset streamed into Kafka in Event Time order. */
export function ReplayForm() {
  const router = useRouter();
  const [speed, setSpeed] = useState("0");
  const [limit, setLimit] = useState("0");
  const [msg, setMsg] = useState("");
  const [pending, start] = useTransition();

  function submit(e: React.FormEvent) {
    e.preventDefault();
    setMsg("");
    start(async () => {
      const res = await fetch("/api/replays", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ profile: PROFILE, speed: Number(speed), limit: Number(limit) }),
      });
      if (!res.ok) {
        setMsg(`Replay not started: ${(await res.text()).trim()}`);
        return;
      }
      setMsg("Replay started.");
      router.refresh();
    });
  }

  return (
    <form className="form" onSubmit={submit}>
      <div className="row">
        <label className="field">Speed (Event Time per second; 0 is as fast as possible)
          <input type="number" min="0" step="any" value={speed} onChange={(e) => setSpeed(e.target.value)} />
        </label>
        <label className="field">Stop after (records; 0 is all)
          <input type="number" min="0" value={limit} onChange={(e) => setLimit(e.target.value)} />
        </label>
        <button type="submit" className="primary" disabled={pending}>Start replay</button>
      </div>
      {msg && <p className={msg.startsWith("Replay started") ? "notice" : "error"} role="status">{msg}</p>}
      <p className="muted" style={{ fontSize: "var(--t-xs)", margin: 0 }}>
        Replays append to the input topic (9.0M payments in all). The scorer and graph-svc must be running to produce decisions.
      </p>
    </form>
  );
}
