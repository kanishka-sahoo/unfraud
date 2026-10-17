"use client";

import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";

const choices = [
  { value: "confirmed", label: "Confirm fraud", done: "Confirmed as fraud", cls: "danger" },
  { value: "dismissed", label: "Dismiss", done: "Dismissed as legitimate", cls: "" },
  { value: "escalated", label: "Escalate", done: "Escalated", cls: "" },
] as const;

/** An analyst's judgment. It is recorded, never used as a Label (ADR-0011). */
export function Disposition({ path, current, note }: { path: string; current: string | null; note: string | null }) {
  const router = useRouter();
  const [text, setText] = useState(note ?? "");
  const [error, setError] = useState("");
  const [pending, start] = useTransition();

  function record(value: string) {
    setError("");
    start(async () => {
      const res = await fetch(`/api/cases/${path}/disposition`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ disposition: value, note: text }),
      });
      if (!res.ok) {
        setError(`Not recorded: ${(await res.text()).trim() || res.statusText}`);
        return;
      }
      router.refresh();
    });
  }

  const done = choices.find((c) => c.value === current);
  return (
    <div className="form">
      {done && <p className="notice" role="status"><b>{done.done}</b>{note ? `: ${note}` : ""}</p>}
      <label className="field">
        Note for the record
        <textarea value={text} onChange={(e) => setText(e.target.value)} maxLength={2000} placeholder="What you checked and why" />
      </label>
      <div className="actions3">
        {choices.map((c) => (
          <button key={c.value} className={c.cls || undefined} disabled={pending} onClick={() => record(c.value)}>
            {c.label}
          </button>
        ))}
      </div>
      {error && <p className="error" role="alert">{error}</p>}
      <p className="muted" style={{ fontSize: "var(--t-xs)", margin: 0 }}>Kept for the record; analyst decisions never feed training or metrics.</p>
    </div>
  );
}
