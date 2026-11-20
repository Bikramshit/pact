"use client";

import { ArrowRight, BookOpen, Bot, CircleCheck, ChartColumn, CirclePause, FileSignature, Fingerprint, KeyRound, Link2, ListChecks, Play, Scale, ScrollText, ShieldAlert, ShieldX, Signature, Target, Workflow, type LucideIcon } from "lucide-react";
import Link from "next/link";
import { useRef } from "react";
import { Feed } from "@/components/Feed";
import { FlowMapDemo } from "@/components/illustrations/FlowMap";
import { ChainArt } from "@/components/illustrations/Spots";
import { Card, CardHeader, Dot, Empty, Skeleton, Stat } from "@/components/ui";
import { ago, compact, short, usePoll } from "@/lib/api";
import { useLive, useLiveEvent } from "@/lib/live";
import type { Agent, EvidenceRecord, ScoreRun, Stats, VerifyResult } from "@/lib/types";

const FEATURES: { icon: LucideIcon; title: string; text: string }[] = [
  { icon: FileSignature, title: "Signed intent contract", text: "Gemini drafts it, you confirm it, the org key signs it." },
  { icon: Scale, title: "Deterministic decisions", text: "Signature checks and an OPA policy. Same input, same answer." },
  { icon: Link2, title: "Tamper-evident evidence", text: "Every allow, deny and hold is hash-chained and signed." },
];

const PIPELINE: { icon: LucideIcon; title: string; text: string }[] = [
  { icon: Fingerprint, title: "Headers", text: "Capability, timestamp, nonce and signature must all be present." },
  { icon: KeyRound, title: "Capability", text: "Issuer signature valid, inside its window, not revoked." },
  { icon: Signature, title: "Request signature", text: "Signed by the agent key the capability is bound to. Fresh nonce." },
  { icon: Target, title: "Derive", text: "The broker works out resource and operation itself." },
  { icon: Scale, title: "Policy", text: "Contract re-verified. OPA decides allow, deny or hold." },
  { icon: Play, title: "Act", text: "Allowed calls run with broker credentials. Denied ones never reach a target." },
  { icon: ScrollText, title: "Record", text: "A signed, chained record is written before the agent gets an answer." },
];

function Meter({ label, value, total, tone }: { label: string; value: number; total: number; tone: "bad" | "ok" }) {
  const pct = total ? (100 * value) / total : 0;
  return (
    <div title={`${label}: ${value} of ${total}`}>
      <div className="mb-1 flex items-baseline justify-between text-[12.5px]">
        <span className="text-ink-2">{label}</span>
        <span className="font-semibold tabular-nums">
          {value} <span className="font-normal text-muted">/ {total}</span>
        </span>
      </div>
      <div className={`h-2 overflow-hidden rounded-full ${tone === "bad" ? "bg-bad-soft" : "bg-ok-soft"}`}>
        <div className={`h-full rounded-full ${tone === "bad" ? "bg-bad" : "bg-ok"}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

export default function OverviewPage() {
  const { status } = useLive();
  const stats = usePoll<Stats>("/v1/stats", 8000);
  const verify = usePoll<VerifyResult>("/v1/evidence/verify", 10000);
  const recent = usePoll<{ records: EvidenceRecord[] }>("/v1/evidence?limit=8", 8000);
  const agents = usePoll<{ agents: Agent[] }>("/v1/agents", 6000);
  const score = usePoll<{ run: ScoreRun | null }>("/v1/scoreboard/latest", 15000);

  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useLiveEvent((ev) => {
    if (ev.type !== "decision" || timer.current) return;
    timer.current = setTimeout(() => {
      timer.current = null;
      stats.refresh();
      recent.refresh();
      verify.refresh();
    }, 1200);
  });

  const s = stats.data;
  // A failed check must not leave an earlier "Verified" on screen.
  const v = verify.error ? null : verify.data;
  const chainLabel = !v ? (verify.error ? "Not checked" : "…") : v.ok ? "Verified" : v.truncated ? "Records deleted" : v.first_bad_seq > 0 ? `Broken at #${v.first_bad_seq}` : "Broken";
  const agent = agents.data?.agents[0];
  const run = score.data?.run;
  const off = run?.summary.off;
  const on = run?.summary.on;

  return (
    <div className="space-y-6">
      {/* Hero */}
      <Card className="overflow-hidden">
        <div className="grid lg:grid-cols-[minmax(0,4fr)_minmax(0,6fr)]">
          <div className="flex flex-col justify-center p-6 sm:p-8">
            <p className="eyebrow mb-3 !text-accent">Proof-carrying autonomy</p>
            <h1 className="font-display text-[30px] leading-[1.12] font-semibold tracking-tight sm:text-[34px]">
              The agent can ask for anything. Only PACT can act.
            </h1>
            <p className="mt-3 text-[15px] leading-relaxed text-ink-2">
              Every tool call carries a capability bound to a contract you signed. The broker checks it, decides deterministically and records the decision, before anything
              touches your systems.
            </p>
            <ul className="mt-6 space-y-3.5">
              {FEATURES.map((f) => (
                <li key={f.title} className="flex gap-3">
                  <span className="grid size-9 shrink-0 place-items-center rounded-lg border border-line bg-raised">
                    <f.icon className="size-4 text-ink-2" />
                  </span>
                  <span>
                    <span className="block font-semibold">{f.title}</span>
                    <span className="block text-[13px] text-ink-2">{f.text}</span>
                  </span>
                </li>
              ))}
            </ul>
            <div className="mt-7 flex flex-wrap gap-2">
              <Link href="/run/" className="btn btn-primary btn-lg">
                <Play className="size-4" /> Run the demo
              </Link>
              <Link href="/docs/" className="btn btn-lg">
                <BookOpen className="size-4" /> Read the docs
              </Link>
              <a href="#pipeline" className="btn btn-lg btn-ghost">
                How it decides <ArrowRight className="size-4" />
              </a>
            </div>
          </div>
          <div className="relative flex flex-col justify-center border-t border-line bg-raised p-3 sm:p-5 lg:border-t-0 lg:border-l">
            <FlowMapDemo />
            <p className="mt-2 text-center text-[12px] text-muted">Illustration: the stage demo’s decisions, replayed on a loop</p>
          </div>
        </div>
      </Card>

      {/* KPIs */}
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {!s ? (
          Array.from({ length: 4 }).map((_, i) => <Skeleton key={i} className="h-[118px] rounded-[14px]" />)
        ) : (
          <>
            <Stat label="Decisions recorded" value={compact(s.records)} icon={ListChecks} hint={`across ${s.tasks} task${s.tasks === 1 ? "" : "s"}${s.last_at ? ` · last ${ago(s.last_at)}` : ""}`} />
            <Stat label="Blocked actions" value={compact(s.denied + s.rejected)} icon={ShieldX} tone="bad" hint={`${s.denied} denied by the broker · ${s.rejected} held and not approved`} />
            <Stat label="Held for approval" value={compact(s.held)} icon={CirclePause} tone="warn" hint={`${s.approved} approved · ${s.rejected} rejected or timed out`} />
            <Stat
              label="Evidence chain"
              value={chainLabel}
              icon={(v && !v.ok) || verify.error ? ShieldAlert : Link2}
              tone={verify.error ? "bad" : !v ? "neutral" : v.ok ? "ok" : "bad"}
              hint={verify.error ? `Could not check the chain: ${verify.error}` : v ? (v.ok ? `${v.count} records · head ${short(v.head_hash, 8) || "empty"}` : v.problem) : "checking…"}
            />
          </>
        )}
      </div>

      <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_380px]">
        <Card className="overflow-hidden">
          <CardHeader
            icon={Workflow}
            title="Recent decisions"
            subtitle="The newest records in the evidence chain"
            actions={
              <Link href="/evidence/" className="btn btn-sm">
                View all <ArrowRight className="size-3.5" />
              </Link>
            }
          />
          {!recent.data ? (
            <div className="space-y-3 p-5">
              {Array.from({ length: 5 }).map((_, i) => (
                <Skeleton key={i} className="h-8" />
              ))}
            </div>
          ) : (
            <Feed
              compact
              items={[...recent.data.records].reverse().map((r) => ({ kind: "record" as const, key: `r${r.seq}`, record: r }))}
              empty={
                <Empty art={<ChainArt />} title="No decisions yet" action={<Link href="/run/" className="btn btn-primary btn-sm">Run a task</Link>}>
                  Run a task and every tool call the broker decides lands here.
                </Empty>
              }
            />
          )}
        </Card>

        <div className="space-y-6">
          <Card>
            <CardHeader icon={Bot} title="Agent" subtitle="Untrusted: holds a capability, never a secret" />
            <div className="space-y-2.5 px-5 py-4 text-[13px]">
              {agent ? (
                <>
                  <div className="flex items-center justify-between">
                    <span className="text-ink-2">Status</span>
                    <span className="flex items-center gap-2 font-medium">
                      <Dot tone={agent.online ? "ok" : "warn"} pulse={agent.online} /> {agent.online ? "Online" : `Last seen ${ago(agent.last_seen)}`}
                    </span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-ink-2">Identity</span>
                    <span className="code">{agent.agent_id}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-ink-2">Public key</span>
                    <span className="code text-muted">{short(agent.pubkey, 14)}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-ink-2">Network</span>
                    <span className="font-medium">Broker only</span>
                  </div>
                </>
              ) : (
                <p className="text-muted">No agent has registered. Start the agent container.</p>
              )}
              <div className="flex items-center justify-between">
                <span className="text-ink-2">Model</span>
                <span className="font-medium">{status ? (status.gemini_configured ? status.model : "Scripted only (no key)") : "…"}</span>
              </div>
            </div>
          </Card>

          <Card>
            <CardHeader
              icon={ChartColumn}
              title="Latest scoreboard"
              subtitle={run ? `${run.scenarios} seed scenarios · ${run.agent_kind === "live" ? "live agent" : "scripted agent"}${run.finished_at ? ` · ${ago(run.finished_at)}` : ""}` : "Same attacks, with and without PACT"}
              actions={
                <Link href="/scoreboard/" className="btn btn-sm">
                  Open <ArrowRight className="size-3.5" />
                </Link>
              }
            />
            <div className="space-y-4 px-5 py-4">
              {off && on ? (
                <>
                  <p className="text-[12.5px] font-medium text-ink-2">Attack attempts where an unauthorised effect happened</p>
                  <Meter label="Without PACT" value={off.attacks_with_effect} total={off.attack_attempts} tone="bad" />
                  <Meter label="With PACT" value={on.attacks_with_effect} total={on.attack_attempts} tone="bad" />
                  <p className="flex items-center gap-1.5 text-[12.5px] text-muted">
                    <CircleCheck className="size-3.5 text-ok" /> Legitimate tasks completed with PACT: {on.tasks_completed} of {on.tasks}
                  </p>
                </>
              ) : (
                <p className="text-[13px] text-muted">{run?.status === "running" ? `Running: ${run.done} of ${run.total} finished…` : "No run yet. A scripted run takes under a minute."}</p>
              )}
            </div>
          </Card>
        </div>
      </div>

      {/* How it decides */}
      <section id="pipeline" className="scroll-mt-6">
        <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
          <div>
            <p className="eyebrow mb-1">Every tool call, the same path</p>
            <h2 className="font-display text-[20px] font-semibold">How the broker decides</h2>
          </div>
          <p className="max-w-md text-[13px] text-ink-2">Any error in verification, policy evaluation or logging means deny. There are no unlogged actions.</p>
        </div>
        <ol className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-7">
          {PIPELINE.map((p, i) => (
            <li key={p.title} className="card relative p-3.5">
              <div className="flex items-center justify-between">
                <span className="grid size-8 place-items-center rounded-lg bg-accent-soft">
                  <p.icon className="size-4 text-accent" />
                </span>
                <span className="code text-muted">{String(i + 1).padStart(2, "0")}</span>
              </div>
              <p className="mt-3 font-semibold">{p.title}</p>
              <p className="mt-1 text-[12.5px] leading-snug text-ink-2">{p.text}</p>
            </li>
          ))}
        </ol>
      </section>
    </div>
  );
}
