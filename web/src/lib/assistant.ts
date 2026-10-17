// The assistant chat's client side: types and the event-stream reader
// (ADR-0022). Safe for client components.
import type { components } from "./schema";

type S = components["schemas"];
export type Investigation = S["Investigation"];
export type InvestigationView = S["InvestigationView"];
export type Turn = S["Turn"];
export type Step = S["Step"];
export type AssistantEvent = S["AssistantEvent"];

/** What the assistant is opened on, from the page's path. */
export type Target = { key: string; caseId?: string; card?: string; label: string };

export function targetFor(path: string): Target {
  const alert = path.match(/^\/alerts\/([^/]+)$/);
  if (alert) {
    const id = decodeURIComponent(alert[1]);
    return { key: `alert:${id}`, caseId: id, label: `alert #${id}` };
  }
  const card = path.match(/^\/network\/([^/]+)$/);
  if (card) {
    const id = decodeURIComponent(card[1]);
    return { key: `card:${id}`, card: id, label: `card ${id}` };
  }
  return { key: "console", label: "the whole console" };
}

async function json<T>(res: Response): Promise<T> {
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
  return (await res.json()) as T;
}

export function createInvestigation(profile: string, t: Target): Promise<InvestigationView> {
  return fetch("/api/investigations", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ profile, case_id: t.caseId, card: t.card }),
  }).then((r) => json<InvestigationView>(r));
}

export function getInvestigation(id: string): Promise<InvestigationView> {
  return fetch(`/api/investigations/${encodeURIComponent(id)}`, { cache: "no-store" }).then((r) => json<InvestigationView>(r));
}

export function listInvestigations(profile: string, caseId?: string): Promise<Investigation[]> {
  const q = new URLSearchParams({ profile });
  if (caseId) q.set("case_id", caseId);
  return fetch(`/api/investigations?${q}`, { cache: "no-store" }).then((r) => json<Investigation[]>(r));
}

/** Asks a question and calls onEvent for each server-sent event until the answer is finished or signal aborts. */
export async function ask(id: string, question: string, fromSeq: number | undefined, signal: AbortSignal,
  onEvent: (e: AssistantEvent) => void): Promise<void> {
  const res = await fetch(`/api/investigations/${encodeURIComponent(id)}/ask`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ question, from_seq: fromSeq }),
    signal,
  });
  if (!res.ok || !res.body) throw new Error((await res.text()).trim() || res.statusText);
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += value;
    let i;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const chunk = buf.slice(0, i);
      buf = buf.slice(i + 2);
      for (const line of chunk.split("\n")) {
        if (line.startsWith("data: ")) onEvent(JSON.parse(line.slice(6)) as AssistantEvent);
      }
    }
  }
}
