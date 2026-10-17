"use client";

import { usePathname } from "next/navigation";
import { createContext, useCallback, useContext, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { Answer } from "@/components/answer";
import {
  ask, createInvestigation, getInvestigation, listInvestigations, targetFor,
  type Investigation, type InvestigationView, type Step, type Target, type Turn,
} from "@/lib/assistant";
import { PROFILE } from "@/lib/format";

type Ctl = { open: boolean; setOpen: (o: boolean) => void; startWith: (q: string) => void };
const AssistantContext = createContext<Ctl>({ open: false, setOpen: () => {}, startWith: () => {} });

/** Lets any page open the assistant, optionally with a question to ask. */
export const useAssistant = () => useContext(AssistantContext);

/** The console shell with the assistant beside the page. */
export function AssistantShell({ sidebar, children }: { sidebar: ReactNode; children: ReactNode }) {
  // Open or closed is remembered across pages and reloads.
  const open = useSyncExternalStore(subscribeOpen, () => localStorage.getItem("assistant-open") === "1", () => false);
  const [queued, setQueued] = useState<string | null>(null);
  const setOpen = useCallback((o: boolean) => {
    localStorage.setItem("assistant-open", o ? "1" : "0");
    window.dispatchEvent(new Event("assistant-open"));
  }, []);
  const startWith = useCallback((q: string) => { setOpen(true); setQueued(q); }, [setOpen]);
  return (
    <AssistantContext.Provider value={{ open, setOpen, startWith }}>
      <div className={`shell${open ? " with-assistant" : ""}`}>
        {sidebar}
        <main className="main">{children}</main>
        {open && <Panel onClose={() => setOpen(false)} queued={queued} clearQueued={() => setQueued(null)} />}
      </div>
      {!open && (
        <button className="assistant-launch primary" onClick={() => setOpen(true)} aria-label="Open the assistant">
          <Sparkle /> Ask the assistant
        </button>
      )}
    </AssistantContext.Provider>
  );
}

function subscribeOpen(onChange: () => void) {
  window.addEventListener("assistant-open", onChange);
  window.addEventListener("storage", onChange);
  return () => {
    window.removeEventListener("assistant-open", onChange);
    window.removeEventListener("storage", onChange);
  };
}

/** A button that opens the assistant, for pages. */
export function AskButton({ question, children }: { question?: string; children: ReactNode }) {
  const { setOpen, startWith } = useAssistant();
  return (
    <button className="ghost" onClick={() => (question ? startWith(question) : setOpen(true))}>
      <Sparkle /> {children}
    </button>
  );
}

// Investigations opened in this browser tab, per page target.
const remembered = (key: string) => sessionStorage.getItem(`assistant:${key}`);
const remember = (key: string, id: string) => sessionStorage.setItem(`assistant:${key}`, id);

type Resolved = { view?: InvestigationView; error?: string };

/** The page's remembered conversation, or a new one. */
async function resolve(t: Target, fresh: boolean): Promise<Resolved> {
  try {
    const id = fresh ? null : remembered(t.key);
    let v: InvestigationView | null = null;
    if (id) v = await getInvestigation(id).catch(() => null);
    if (!v) {
      v = await createInvestigation(PROFILE, t);
      remember(t.key, v.id);
    }
    return { view: v };
  } catch (e) {
    return { error: e instanceof Error && e.message.includes("off") ? "The assistant is turned off on this console." :
      "The assistant is unavailable right now. Try again in a moment." };
  }
}

/** A question being answered: what the stream has said so far. */
type Pending = { question: string; fromSeq?: number; seq?: number; steps: Step[]; text: string; error?: string };

function Panel({ onClose, queued, clearQueued }: { onClose: () => void; queued: string | null; clearQueued: () => void }) {
  const target = targetFor(usePathname() ?? "");
  const [view, setView] = useState<InvestigationView | null>(null);
  const [loadError, setLoadError] = useState("");
  const [pending, setPending] = useState<Pending | null>(null);
  const [history, setHistory] = useState<Investigation[] | null>(null);
  const [status, setStatus] = useState("");
  const abort = useRef<AbortController | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const busy = pending !== null && !pending.error;

  const apply = useCallback((r: Resolved) => {
    if (r.view) setView(r.view);
    setLoadError(r.error ?? "");
    setHistory(null);
  }, []);
  const load = useCallback((t: Target, fresh: boolean) => resolve(t, fresh).then(apply), [apply]);

  // Follow the page: each alert or card has its own conversation.
  useEffect(() => {
    if (busy) return;
    let live = true;
    resolve(target, false).then((r) => { if (live) apply(r); });
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target.key]);

  useEffect(() => {
    scroller.current?.scrollTo({ top: scroller.current.scrollHeight, behavior: "smooth" });
  }, [view?.turns.length, pending?.text, pending?.steps.length]);

  const send = useCallback(async (question: string, fromSeq?: number) => {
    if (!view || busy || !question.trim()) return;
    const ctl = new AbortController();
    abort.current = ctl;
    setStatus("Working on it…");
    setPending({ question, fromSeq, steps: [], text: "" });
    let failed = "";
    let seq: number | undefined;
    try {
      await ask(view.id, question, fromSeq, ctl.signal, (e) => {
        if (e.type === "error") {
          failed = e.message ?? "Something went wrong.";
          return;
        }
        if (e.type === "question") seq = e.seq;
        setPending((p) => {
          if (!p) return p;
          switch (e.type) {
            case "question":
              return { ...p, seq: e.seq };
            case "text":
              return { ...p, text: p.text + (e.text ?? "") };
            case "step": {
              const s = e.step!;
              let steps = p.steps;
              let text = p.text;
              // Text written before a lookup was the assistant thinking aloud.
              if (text.trim() && s.status === "running") {
                steps = [...steps, { id: `note-${steps.length}`, status: "note", note: text.trim() }];
                text = "";
              }
              const i = steps.findIndex((x) => x.id === s.id);
              steps = i >= 0 ? steps.map((x, j) => (j === i ? s : x)) : [...steps, s];
              return { ...p, steps, text };
            }
          }
          return p;
        });
      });
    } catch (e) {
      if (!ctl.signal.aborted) failed = e instanceof Error ? e.message : "The assistant is unavailable right now.";
    }
    if (ctl.signal.aborted) {
      // The service records the stop; give it a moment.
      await new Promise((r) => setTimeout(r, 600));
    }
    const fresh = await getInvestigation(view.id).catch(() => null);
    if (fresh) setView(fresh);
    abort.current = null;
    // A failure the service recorded shows as that turn; others stay here.
    if (failed && !fresh?.turns.some((t) => t.seq === seq)) {
      setPending((p) => (p ? { ...p, error: failed } : p));
    } else {
      setPending(null);
    }
    setStatus(ctl.signal.aborted ? "Stopped." : failed || "Answer ready.");
  }, [view, busy]);

  // A question a page asked us to start with.
  useEffect(() => {
    if (!queued || !view || busy) return;
    const t = setTimeout(() => { clearQueued(); send(queued); });
    return () => clearTimeout(t);
  }, [queued, view, busy, send, clearQueued]);

  const stop = () => abort.current?.abort();

  const newChat = () => {
    if (busy) return;
    setPending(null);
    load(target, true);
  };

  const showHistory = async () => {
    if (history) return setHistory(null);
    setHistory(await listInvestigations(PROFILE, target.caseId).catch(() => []));
  };

  const open = async (id: string) => {
    setHistory(null);
    const v = await getInvestigation(id).catch(() => null);
    if (v) {
      remember(target.key, id);
      setView(v);
      setPending(null);
    }
  };

  // While a question is rewound, the turns it replaces are hidden.
  const turns = (view?.turns ?? []).filter((t) => !pending || pending.fromSeq === undefined || t.seq < pending.fromSeq)
    .filter((t) => !pending || t.seq !== pending.seq);
  const over = view && view.token_budget > 0 && view.tokens_used >= view.token_budget;
  const nearly = view && view.token_budget > 0 && !over && view.tokens_used >= view.token_budget * 0.8;

  return (
    <aside className="assistant" aria-label="Assistant">
      <header>
        <div className="title">
          <b><Sparkle /> Assistant</b>
          <small>{view?.case_id || view?.card ? `About ${view.case_id ? `alert #${view.case_id}` : `card ${view.card}`}` : `About ${target.label}`}</small>
        </div>
        <div className="tools">
          <button className="ghost icon" onClick={showHistory} aria-expanded={history !== null} title="Earlier conversations" aria-label="Earlier conversations">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5M12 7v5l3 2" /></svg>
          </button>
          <button className="ghost icon" onClick={newChat} disabled={busy} title="New chat" aria-label="New chat">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
          </button>
          <button className="ghost icon" onClick={onClose} title="Close" aria-label="Close the assistant">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18" /></svg>
          </button>
        </div>
      </header>

      {history !== null && (
        <div className="history">
          <h3>Earlier conversations {target.caseId ? "about this alert" : ""}</h3>
          {history.length === 0 ? <p className="dim">None yet.</p> : (
            <ul>
              {history.map((h) => (
                <li key={h.id}>
                  <button className="ghost" onClick={() => open(h.id)} aria-current={h.id === view?.id ? "true" : undefined}>
                    <span>{h.title || "Untitled"}</span>
                    <small>{new Date(h.updated_at).toLocaleString("en-US", { dateStyle: "medium", timeStyle: "short" })}</small>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      <div className="convo" ref={scroller}>
        {loadError && <p className="error" role="alert">{loadError}</p>}
        {view && turns.length === 0 && !pending && (
          <div className="intro">
            <p>Ask about {view.case_id ? "this alert" : view.card ? "this card" : "any alert, card or merchant"} in plain words.
              I look things up in the console&apos;s data, show each step, and mark any number I couldn&apos;t check.</p>
            <div className="suggest">
              {view.suggestions.map((s) => <button key={s} className="ghost" onClick={() => send(s)}>{s}</button>)}
            </div>
            {view.model === "stub" && (
              <p className="notice">No AI model is set up, so answers come from a fixed script. An administrator can turn the
                assistant on by adding an Anthropic API key (ANTHROPIC_API_KEY in .env, then make secrets).</p>
            )}
          </div>
        )}
        {turns.map((t, i) => (
          <TurnView key={t.seq} turn={t} last={i === turns.length - 1 && !pending} busy={busy}
            onResend={(q) => send(q, t.seq)} />
        ))}
        {pending && <PendingView p={pending} onRetry={() => send(pending.question, pending.seq ?? pending.fromSeq)} />}
      </div>

      <Composer busy={busy} disabled={!view || !!over} onSend={(q) => send(q)} onStop={stop}
        note={over ? "This conversation has reached its length limit. Start a new chat to keep going." :
          nearly ? "This conversation is getting long; a new chat will answer faster." : undefined} />
      <p className="sr-only" role="status" aria-live="polite">{status}</p>
    </aside>
  );
}

function TurnView({ turn, last, busy, onResend }: { turn: Turn; last: boolean; busy: boolean; onResend: (q: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(turn.question);
  const [copied, setCopied] = useState(false);
  return (
    <div className="turn">
      {editing ? (
        <form className="q editing" onSubmit={(e) => { e.preventDefault(); setEditing(false); onResend(draft); }}>
          <textarea value={draft} onChange={(e) => setDraft(e.target.value)} aria-label="Edit your question" autoFocus
            onKeyDown={(e) => { if (e.key === "Escape") setEditing(false); }} />
          <small>Sending replaces this answer and everything after it.</small>
          <div className="row">
            <button type="button" className="ghost" onClick={() => setEditing(false)}>Cancel</button>
            <button type="submit" className="primary" disabled={!draft.trim()}>Send</button>
          </div>
        </form>
      ) : (
        <div className="q">
          <p>{turn.question}</p>
          <div className="q-actions">
            <button className="ghost icon" disabled={busy} onClick={() => { setDraft(turn.question); setEditing(true); }} title="Edit and resend" aria-label="Edit and resend">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="M4 20h4L19 9l-4-4L4 16zM14 6l4 4" /></svg>
            </button>
          </div>
        </div>
      )}
      <Steps steps={turn.steps} running={false} />
      {turn.answer && (turn.status === "answered" ? (
        <Answer text={turn.answer} links={turn.links} unverified={turn.unverified} />
      ) : (
        <p className={`a-note ${turn.status}`}>{turn.answer}</p>
      ))}
      <div className="a-foot">
        {turn.status === "answered" && turn.unverified.length > 0 && (
          <span className="warn">{turn.unverified.length === 1 ? "1 detail" : `${turn.unverified.length} details`} not verified</span>
        )}
        {turn.status === "answered" && (
          <button className="ghost small" onClick={() => {
            navigator.clipboard.writeText(turn.answer).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); });
          }}>{copied ? "Copied" : "Copy"}</button>
        )}
        {last && turn.status !== "pending" && (
          <button className="ghost small" disabled={busy} onClick={() => onResend(turn.question)}>Retry</button>
        )}
      </div>
    </div>
  );
}

function PendingView({ p, onRetry }: { p: Pending; onRetry: () => void }) {
  const working = !p.error;
  return (
    <div className="turn" aria-busy={working}>
      <div className="q"><p>{p.question}</p></div>
      <Steps steps={p.steps} running={working} />
      {p.text ? <Answer text={p.text} links={{}} unverified={[]} /> : working && (
        <p className="thinking"><span className="dots" aria-hidden="true"><i /><i /><i /></span>
          {p.steps.some((s) => s.status === "running") ? "Looking things up…" : "Thinking…"}</p>
      )}
      {p.error && (
        <div className="a-note error" role="alert">
          {p.error} <button className="ghost small" onClick={onRetry}>Try again</button>
        </div>
      )}
    </div>
  );
}

function Steps({ steps, running }: { steps: Step[]; running: boolean }) {
  if (steps.length === 0) return null;
  const lookups = steps.filter((s) => s.status !== "note").length;
  return (
    <details className="steps" open={running || undefined}>
      <summary>{running ? "Working it out" : `How I worked this out · ${lookups === 1 ? "1 lookup" : `${lookups} lookups`}`}</summary>
      <ol>
        {steps.map((s) => (
          <li key={s.id} className={s.status}>
            {s.status === "note" ? <p className="note">{s.note}</p> : (
              <>
                <span className="icon" aria-hidden="true">{s.status === "running" ? <span className="spin" /> : s.status === "done" ? "✓" : "!"}</span>
                <div>
                  <div className="label">{s.label}</div>
                  {s.summary && <div className="sum">{s.summary}</div>}
                  {s.error && <div className="sum err">{s.error}</div>}
                  {s.table && s.table.rows.length > 0 && (
                    <details className="data">
                      <summary>Show what I found</summary>
                      <div className="table-wrap">
                        <table>
                          {s.table.columns.some(Boolean) && (
                            <thead><tr>{s.table.columns.map((c, i) => <th key={i}>{c}</th>)}</tr></thead>
                          )}
                          <tbody>
                            {s.table.rows.map((r, i) => (
                              <tr key={i}>{r.map((c, j) => (
                                <td key={j} className={c.tone}>{c.href ? <a className="link" href={c.href}>{c.text}</a> : c.text}</td>
                              ))}</tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                      {!!s.table.more && <small>and {s.table.more} more</small>}
                    </details>
                  )}
                </div>
              </>
            )}
          </li>
        ))}
      </ol>
    </details>
  );
}

function Composer({ busy, disabled, onSend, onStop, note }: {
  busy: boolean; disabled: boolean; onSend: (q: string) => void; onStop: () => void; note?: string;
}) {
  const [q, setQ] = useState("");
  const submit = () => {
    if (!q.trim() || busy || disabled) return;
    onSend(q.trim());
    setQ("");
  };
  return (
    <form className="composer" onSubmit={(e) => { e.preventDefault(); submit(); }}>
      {note && <p className="notice">{note}</p>}
      <div className="box">
        <textarea value={q} onChange={(e) => setQ(e.target.value)} placeholder="Ask about an alert, card or merchant…" rows={2}
          aria-label="Your question" disabled={disabled}
          onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); submit(); } }} />
        {busy ? (
          <button type="button" className="stop" onClick={onStop} aria-label="Stop answering">
            <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><rect width="12" height="12" rx="2" fill="currentColor" /></svg> Stop
          </button>
        ) : (
          <button type="submit" className="primary" disabled={!q.trim() || disabled} aria-label="Send">Send</button>
        )}
      </div>
      <small>Answers can be wrong. The assistant only reads data; you make the decision.</small>
    </form>
  );
}

function Sparkle() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z" />
    </svg>
  );
}
