"use client";

import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";

const profiles = [
  ["ieee-cis", "IEEE-CIS card payments (590,540 events)"],
  ["elliptic", "Elliptic Bitcoin (203,769 transactions)"],
  ["paysim", "PaySim mobile money (6.36M events, load testing)"],
];

/** Starts a replay Job: a dataset streamed into Kafka in Event Time order. */
export function ReplayForm() {
  const router = useRouter();
  const [profile, setProfile] = useState("elliptic");
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
        body: JSON.stringify({ profile, speed: Number(speed), limit: Number(limit) }),
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
        <label className="field">Dataset
          <select value={profile} onChange={(e) => setProfile(e.target.value)}>
            {profiles.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
          </select>
        </label>
        <label className="field">Speed (Event Time per second; 0 is as fast as possible)
          <input type="number" min="0" step="any" value={speed} onChange={(e) => setSpeed(e.target.value)} />
        </label>
        <label className="field">Stop after (records; 0 is all)
          <input type="number" min="0" value={limit} onChange={(e) => setLimit(e.target.value)} />
        </label>
        <button type="submit" disabled={pending}>Start replay</button>
      </div>
      {msg && <p className={msg.startsWith("Replay started") ? "notice" : "error"} role="status">{msg}</p>}
      <p className="muted" style={{ fontSize: "var(--t-xs)", margin: 0 }}>
        Replays append to the dataset&apos;s input topic. The scorer and graph-svc must be running for that dataset to produce Decisions.
      </p>
    </form>
  );
}
