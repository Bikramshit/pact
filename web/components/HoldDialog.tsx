"use client";

import { diffLines } from "diff";
import { ArrowRight, CircleCheck, CirclePause, Gavel, Minimize2, Timer, TriangleAlert } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, repoPath } from "@/lib/api";
import { useLive } from "@/lib/live";
import { useToast } from "@/lib/toast";
import type { Hold } from "@/lib/types";

/** Words from the held action worth pointing at in the untrusted text. */
function needles(hold: Hold): string[] {
  const out = new Set<string>([hold.tool]);
  const path = repoPath(hold.resource).replace(/^net:/, "");
  if (path) out.add(path);
  if (hold.after !== undefined) {
    const before = hold.before ?? "";
    for (const m of hold.after.matchAll(/https?:\/\/[^\s"'`)]+|name:\s*([\w-]+)/g)) {
      const token = m[1] ?? m[0];
      if (!before.includes(token)) out.add(token);
    }
  }
  if (typeof hold.args?.url === "string") out.add(hold.args.url as string);
  return [...out].filter((n) => n.length >= 4);
}

function Diff({ before, after }: { before: string; after: string }) {
  const parts = useMemo(() => diffLines(before, after), [before, after]);
  let added = 0;
  let removed = 0;
  parts.forEach((p) => {
    const n = p.value.replace(/\n$/, "").split("\n").length;
    if (p.added) added += n;
    if (p.removed) removed += n;
  });
  return (
    <div className="overflow-hidden rounded-xl border border-line">
      <div className="flex items-center justify-between border-b border-line bg-raised px-3 py-1.5 text-[12px]">
        <span className="font-medium text-ink-2">Proposed file</span>
        <span className="code">
          <span className="text-ok">+{added}</span> <span className="text-bad">−{removed}</span>
        </span>
      </div>
      <pre className="code scroll-thin max-h-72 overflow-auto bg-surface py-1 leading-5 break-words whitespace-pre-wrap">
        {parts.map((p, i) =>
          p.value
            .replace(/\n$/, "")
            .split("\n")
            .map((line, j) => (
              <div key={`${i}-${j}`} className={`flex ${p.added ? "bg-ok-soft" : p.removed ? "bg-bad-soft" : ""}`}>
                <span className={`w-7 shrink-0 text-center select-none ${p.added ? "text-ok" : p.removed ? "text-bad" : "text-muted"}`}>{p.added ? "+" : p.removed ? "−" : ""}</span>
                <span className={`pr-3 ${p.added || p.removed ? "text-ink" : "text-ink-2"}`}>{line || " "}</span>
              </div>
            )),
        )}
      </pre>
    </div>
  );
}

function Source({ excerpt, marks }: { excerpt: string; marks: string[] }) {
  const lines = excerpt.split("\n");
  const hit = (line: string) => marks.some((m) => line.includes(m));
  const first = lines.findIndex(hit);
  const box = useRef<HTMLPreElement>(null);
  const firstHit = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (box.current && firstHit.current) box.current.scrollTop = Math.max(0, firstHit.current.offsetTop - box.current.offsetTop - 44);
  }, [excerpt, first]);
  return (
    <pre ref={box} className="code scroll-thin max-h-72 overflow-auto bg-surface py-1 leading-5 break-words whitespace-pre-wrap">
      {lines.map((line, i) => (
        <div key={i} ref={i === first ? firstHit : undefined} className={hit(line) ? "border-l-2 border-warn bg-warn-soft px-3 text-ink" : "border-l-2 border-transparent px-3 text-muted"}>
          {line || " "}
        </div>
      ))}
    </pre>
  );
}

function Countdown({ deadline }: { deadline: string }) {
  const [left, setLeft] = useState(0);
  useEffect(() => {
    const tick = () => setLeft(Math.max(0, Math.round((new Date(deadline).getTime() - Date.now()) / 1000)));
    tick();
    const t = setInterval(tick, 500);
    return () => clearInterval(t);
  }, [deadline]);
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full bg-subtle px-2.5 py-1 text-[12.5px] font-medium tabular-nums">
      <Timer className="size-3.5 text-muted" />
      {left}s, then rejected
    </span>
  );
}

/** The chain of facts that led to a hold, in the order they happened. */
function why(hold: Hold) {
  const steps = [{ icon: CircleCheck, tone: "text-ok", text: "The contract allows this action" }];
  if (hold.reasons.includes("confidential_session")) steps.push({ icon: TriangleAlert, tone: "text-warn", text: "The agent read confidential data first" });
  if (hold.reasons.includes("tainted_session")) steps.push({ icon: TriangleAlert, tone: "text-warn", text: "The agent read untrusted content first" });
  const last = hold.reasons.includes("confidential_session") ? "It sends data out: held for a human" : hold.reasons.includes("tainted_session") ? "Sensitive action: held for a human" : "The contract requires approval";
  steps.push({ icon: CirclePause, tone: "text-warn", text: last });
  return steps;
}

/**
 * Global approval centre. A held action opens this dialog on whichever page the
 * judge is looking at; "Decide later" shrinks it to a pill that keeps pulsing.
 */
export function HoldCenter() {
  const { holds, refreshHolds } = useLive();
  const toast = useToast();
  // Oldest first: the one closest to its timeout. The hold on screen stays on
  // screen until it is resolved, so a new hold can never replace the one a
  // judge is reading (and Approve can never act on a hold they did not see).
  const pending = useMemo(
    () =>
      holds
        .filter((h) => h.status === "pending")
        .sort((a, b) => a.opened_at.localeCompare(b.opened_at) || a.hold_id.localeCompare(b.hold_id)),
    [holds],
  );
  const [shownId, setShownId] = useState<string | null>(null);
  const hold = pending.find((h) => h.hold_id === shownId) ?? pending[0];
  const [minimized, setMinimized] = useState<string | null>(null);
  const [by, setBy] = useState("judge");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const marks = useMemo(() => (hold ? needles(hold) : []), [hold]);
  const open = !!hold && minimized !== hold.hold_id;

  useEffect(() => {
    const id = hold?.hold_id ?? null;
    if (id !== shownId) {
      setShownId(id);
      setError(null);
    }
  }, [hold?.hold_id, shownId]);

  useEffect(() => {
    try {
      const saved = localStorage.getItem("pact-approver");
      if (saved) setBy(saved);
    } catch {
      /* storage unavailable */
    }
  }, []);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && hold && setMinimized(hold.hold_id);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, hold]);

  if (!hold) return null;

  const resolve = async (action: "approve" | "reject") => {
    setBusy(true);
    setError(null);
    try {
      await api(`/v1/holds/${hold.hold_id}/${action}`, { by: by.trim() });
      try {
        localStorage.setItem("pact-approver", by.trim());
      } catch {
        /* storage unavailable */
      }
      toast({
        tone: action === "reject" ? "ok" : "warn",
        title: action === "reject" ? "Rejected. Nothing was executed." : "Approved. The action was executed.",
        body: `${hold.tool} ${repoPath(hold.resource)} · recorded in the evidence chain as decided by ${by.trim()}`,
      });
      refreshHolds();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const target = hold.tool === "http_request" ? `${hold.resource.replace(/^net:/, "")}` : repoPath(hold.resource);
  const verb = hold.tool === "http_request" ? `send a ${hold.op} request to` : hold.op;

  if (!open) {
    return (
      <button onClick={() => setMinimized(null)} className="ring-pulse fixed bottom-5 left-1/2 z-50 flex -translate-x-1/2 animate-rise items-center gap-3 rounded-full border border-warn/40 bg-surface py-2 pr-2 pl-4 shadow-pop">
        <CirclePause className="size-4 text-warn" />
        <span className="text-[13px]">
          <b>Approval needed</b> · {hold.tool} <span className="code">{target}</span>
        </span>
        <span className="inline-flex items-center gap-1 rounded-full bg-inverse px-3 py-1 text-[12.5px] font-semibold text-on-inverse">
          Review <ArrowRight className="size-3.5" />
        </span>
      </button>
    );
  }

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 backdrop-blur-[2px] sm:items-center" role="dialog" aria-modal="true" aria-labelledby="hold-title">
      <div className="my-auto w-full max-w-5xl animate-rise rounded-2xl border border-line bg-surface shadow-pop">
        <div className="flex flex-wrap items-start gap-4 border-b border-line p-5">
          <span className="ring-pulse grid size-11 shrink-0 place-items-center rounded-xl bg-warn-soft">
            <Gavel className="size-5 text-warn" />
          </span>
          <div className="min-w-0 flex-1">
            <p className="eyebrow">Approval needed · {hold.hold_id}</p>
            <h2 id="hold-title" className="font-display mt-0.5 text-[21px] leading-snug font-semibold">
              The agent wants to {verb} <span className="code text-[18px]">{target}</span>
            </h2>
            <p className="mt-1 text-ink-2">Nothing has executed. The request is paused inside the broker until you decide.</p>
          </div>
          <div className="flex items-center gap-2">
            <Countdown deadline={hold.deadline} />
            <button className="btn btn-ghost btn-sm" onClick={() => setMinimized(hold.hold_id)} title="Decide later (Esc)">
              <Minimize2 className="size-4" />
              <span className="hidden sm:inline">Decide later</span>
            </button>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2 border-b border-line bg-raised px-5 py-3">
          {why(hold).map((w, i) => (
            <span key={w.text} className="flex items-center gap-2">
              {i > 0 && <ArrowRight className="size-3.5 text-muted" />}
              <span className="inline-flex items-center gap-1.5 rounded-lg border border-line bg-surface px-2.5 py-1 text-[12.5px] font-medium">
                <w.icon className={`size-3.5 ${w.tone}`} />
                {w.text}
              </span>
            </span>
          ))}
        </div>

        <div className="grid gap-5 p-5 lg:grid-cols-2">
          <div className="min-w-0">
            <p className="mb-2 text-[13px] font-semibold">{hold.after !== undefined ? "Requested change" : "Request"}</p>
            {hold.after !== undefined ? (
              <Diff before={hold.before ?? ""} after={hold.after} />
            ) : (
              <pre className="code overflow-auto rounded-xl border border-line bg-raised p-3">{JSON.stringify(hold.args, null, 2)}</pre>
            )}
          </div>
          <div className="min-w-0">
            <p className="mb-2 text-[13px] font-semibold">{hold.taint_sources.length > 0 ? "Untrusted content the agent read first" : "What the agent read first"}</p>
            {(hold.confidential_sources ?? []).map((src) => (
              <div key={src} className="mb-2 flex items-start gap-2.5 rounded-xl border border-warn/30 bg-warn-soft px-3 py-2.5 text-[13px]">
                <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warn" />
                <span>
                  Confidential: <span className="code">{src}</span>
                  <span className="block text-[12px] text-ink-2">Its value is not shown here and is not stored by the broker. Check the request on the left for it.</span>
                </span>
              </div>
            ))}
            {hold.taint_sources.length === 0 && !(hold.confidential_sources?.length) && <p className="text-muted">Nothing untrusted or confidential: held because the contract requires approval for this action.</p>}
            {hold.taint_sources.map((t) => (
              <div key={t.seq} className="overflow-hidden rounded-xl border border-line">
                <div className="flex items-center justify-between gap-2 border-b border-line bg-raised px-3 py-1.5 text-[12px]">
                  <span className="code truncate font-medium text-ink">{repoPath(t.resource)}</span>
                  <span className="shrink-0 text-muted">record #{t.seq}</span>
                </div>
                <Source excerpt={t.excerpt ?? ""} marks={marks} />
              </div>
            ))}
            {hold.taint_sources.length > 0 && (
              <p className="mt-2 text-[12px] text-muted">
                Highlighted lines mention the held action. PACT does not detect prompt injection: the hold comes from the taint rule alone.
              </p>
            )}
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-3 border-t border-line p-5">
          <label className="flex items-center gap-2 text-[13px] text-ink-2">
            Decided by
            <input className="input !h-9 !w-40 !py-0" value={by} onChange={(e) => setBy(e.target.value)} aria-label="Approver name" />
          </label>
          <span className="hidden flex-1 text-[12.5px] text-muted md:block">Your decision and name are signed into the evidence chain.</span>
          {error && <span className="text-[13px] text-bad">{error}</span>}
          <div className="ml-auto flex gap-2">
            <button className="btn" disabled={busy || !by.trim()} onClick={() => resolve("approve")}>
              Approve and execute
            </button>
            <button className="btn btn-danger btn-lg" disabled={busy || !by.trim()} onClick={() => resolve("reject")} autoFocus>
              Reject
            </button>
          </div>
        </div>
        {pending.length > 1 && <p className="border-t border-line px-5 py-2 text-[12.5px] text-muted">{pending.length - 1} more held action(s) waiting. Each opens here when this one is decided.</p>}
      </div>
    </div>
  );
}
