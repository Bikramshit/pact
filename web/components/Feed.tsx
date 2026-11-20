"use client";

import { Ban, CircleCheck, CirclePause, CircleX, Sparkles, TriangleAlert } from "lucide-react";
import { clock, repoPath } from "@/lib/api";
import type { EvidenceRecord } from "@/lib/types";
import { explain, verdict } from "./Decision";
import { toneSoft, toneText, type Tone } from "./ui";

export type FeedItem =
  | { kind: "record"; key: string; record: EvidenceRecord }
  | { kind: "unprotected"; key: string; at: string; tool: string; resource: string; op: string; outcome: string };

const ICON = { ok: CircleCheck, bad: CircleX, warn: CirclePause, neutral: Ban, accent: CircleCheck };

function Glyph({ tone, model }: { tone: Tone; model?: boolean }) {
  const Icon = model ? Sparkles : ICON[tone];
  return (
    <span className={`grid size-7 shrink-0 place-items-center rounded-full ${model ? "bg-subtle" : toneSoft(tone)}`}>
      <Icon className={`size-[15px] ${model ? "text-muted" : toneText(tone)}`} strokeWidth={2.1} />
    </span>
  );
}

export function Feed({ items, empty, compact }: { items: FeedItem[]; empty: React.ReactNode; compact?: boolean }) {
  if (items.length === 0) return <>{empty}</>;
  return (
    <ol className="divide-y divide-line">
      {items.map((item) => (item.kind === "record" ? <RecordRow key={item.key} r={item.record} compact={compact} /> : <UnprotectedRow key={item.key} item={item} />))}
    </ol>
  );
}

function Target({ resource, op }: { resource: string; op: string }) {
  const shown = resource.startsWith("repo:") ? repoPath(resource) || "/" : resource;
  return (
    <span className="code truncate text-ink-2" title={`${resource}:${op}`}>
      {shown}
    </span>
  );
}

function RecordRow({ r, compact }: { r: EvidenceRecord; compact?: boolean }) {
  const v = verdict(r);
  const model = r.request.tool === "llm_generate";
  return (
    <li className={`flex animate-rise gap-3 px-5 ${compact ? "py-2.5" : "py-3"} ${model ? "opacity-60" : ""}`}>
      <Glyph tone={v.tone} model={model && v.tone === "ok"} />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className="shrink-0 font-semibold">{r.request.tool}</span>
          <Target resource={r.request.resource} op={r.request.op} />
          <span className="ml-auto shrink-0 pl-2 text-[12px] text-muted tabular-nums">{clock(r.ts)}</span>
        </div>
        {!compact && (
          <div className="mt-0.5 space-y-0.5 text-[12.5px] text-ink-2">
            <p>
              <span className="font-medium text-ink">{v.text}</span>
              {r.reasons.length > 0 && <> · {r.reasons.map(explain).join("; ")}</>}
              {r.approval && (
                <>
                  {" "}by <span className="font-medium text-ink">{r.approval.by}</span>
                </>
              )}
              {r.outcome === "execution_failed" && " · the action itself failed"}
            </p>
            {r.taint_added && (
              <p className="text-ink">
                <TriangleAlert className="mr-1.5 inline size-3.5 align-[-2px] text-warn" />
                Untrusted content read: the session is now tainted by <span className="code">{repoPath(r.taint_added.resource)}</span>
              </p>
            )}
            {r.confidential_added && (
              <p className="text-ink">
                <TriangleAlert className="mr-1.5 inline size-3.5 align-[-2px] text-warn" />
                Confidential data read: outbound requests are now gated (<span className="code">{r.confidential_added}</span>)
              </p>
            )}
            {r.decision === "hold" && !r.approval && r.taint_sources && r.taint_sources.length > 0 && (
              <p className="text-muted">
                Tainted by <span className="code">{repoPath(r.taint_sources[0].resource)}</span> at record #{r.taint_sources[0].seq}
              </p>
            )}
          </div>
        )}
      </div>
      <span className="hidden w-12 shrink-0 text-right text-[12px] text-muted tabular-nums sm:block">#{r.seq}</span>
    </li>
  );
}

function UnprotectedRow({ item }: { item: Extract<FeedItem, { kind: "unprotected" }> }) {
  return (
    <li className="flex animate-rise gap-3 px-5 py-3">
      <Glyph tone="neutral" />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className="font-semibold">{item.tool}</span>
          <Target resource={item.resource} op={item.op} />
          <span className="ml-auto shrink-0 pl-2 text-[12px] text-muted tabular-nums">{clock(item.at)}</span>
        </div>
        <p className="mt-0.5 text-[12.5px] text-ink-2">PACT is off: executed with no check and no evidence record</p>
      </div>
      <span className="hidden w-12 shrink-0 sm:block" />
    </li>
  );
}
