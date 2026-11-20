"use client";

import { CircleCheck, CirclePause, CircleX, Clock, FlaskConical, Info, Play, Sparkles, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { ScoreArt } from "@/components/illustrations/Spots";
import { UseOwnKeyButton, billedTo, brokenServerKey } from "@/components/ModelKeyDialog";
import { EFFECTS, MODES, MODE_ORDER, Outcome, latency, modelsUsed } from "@/components/Score";
import { Card, CardHeader, Empty, PageHeader, Segmented, Skeleton, Spinner, StatusBadge } from "@/components/ui";
import { api, dateTime, usePoll } from "@/lib/api";
import { useLive, useLiveEvent } from "@/lib/live";
import { useToast } from "@/lib/toast";
import type { Mode, RunInfo, ScoreResult, ScoreRun, ScoreSummary } from "@/lib/types";

/** A labelled ratio bar: the fill carries the state, the track is a lighter step of the same hue. */
function Meter({ label, value, total, tone, note }: { label: React.ReactNode; value: number; total: number; tone: "bad" | "ok"; note?: string }) {
  const pct = total ? (100 * value) / total : 0;
  return (
    <div title={`${value} of ${total}`}>
      <div className="mb-1.5 flex items-baseline justify-between gap-3">
        <span className="text-[13px] text-ink-2">{label}</span>
        <span className="text-[13px] font-semibold tabular-nums">
          {value} <span className="font-normal text-muted">of {total}</span>
        </span>
      </div>
      <div className={`h-2.5 overflow-hidden rounded-full ${tone === "bad" ? "bg-bad-soft" : "bg-ok-soft"}`}>
        <div className={`h-full rounded-full transition-[width] duration-500 ${tone === "bad" ? "bg-bad" : "bg-ok"}`} style={{ width: `${pct}%` }} />
      </div>
      {note && <p className="mt-1 text-[12px] text-muted">{note}</p>}
    </div>
  );
}

function Figure({ label, value, note }: { label: string; value: React.ReactNode; note?: string }) {
  return (
    <div>
      <p className="text-[13px] text-ink-2">{label}</p>
      <p className="mt-0.5 text-[22px] leading-tight font-semibold">{value}</p>
      {note && <p className="text-[12px] text-muted">{note}</p>}
    </div>
  );
}

function Column({ mode, s }: { mode: Mode; s?: ScoreSummary }) {
  const meta = MODES[mode];
  const Icon = meta.icon;
  const on = mode === "on";
  return (
    <Card className={on ? "ring-1 ring-accent/40" : ""}>
      <div className="flex items-center gap-3 border-b border-line px-5 py-4">
        <span className={`grid size-9 shrink-0 place-items-center rounded-lg ${on ? "bg-accent text-white" : "bg-subtle text-ink-2"}`}>
          <Icon className="size-[18px]" />
        </span>
        <div className="min-w-0">
          <h2 className="font-display text-[18px] font-semibold">{meta.title}</h2>
          <p className="truncate text-[12px] text-muted">{meta.sub}</p>
        </div>
      </div>
      {!s ? (
        <p className="px-5 py-8 text-center text-muted">No results for this configuration yet.</p>
      ) : (
        <div className="space-y-5 p-5">
          <div>
            <p className="text-[13px] text-ink-2">Attack attempts where an unauthorised effect happened</p>
            <p className="mt-1 flex flex-wrap items-baseline gap-2">
              <span className="text-[44px] leading-none font-semibold tracking-tight">{s.attacks_with_effect}</span>
              <span className="text-[18px] text-muted">of {s.attack_attempts}</span>
              {s.attacks_with_effect === 0 ? <StatusBadge tone="ok">none landed</StatusBadge> : <StatusBadge tone="bad">landed</StatusBadge>}
            </p>
            <div className="mt-3 h-2.5 overflow-hidden rounded-full bg-bad-soft">
              <div className="h-full rounded-full bg-bad transition-[width] duration-500" style={{ width: `${s.attack_attempts ? (100 * s.attacks_with_effect) / s.attack_attempts : 0}%` }} />
            </div>
            <p className="mt-1.5 text-[12px] text-muted">{s.unauthorized_effects} unauthorised effect(s) observed on the targets in total</p>
          </div>
          <Meter label="Legitimate tasks completed" value={s.tasks_completed} total={s.tasks} tone="ok" note="Deploy target holds a release whose tests passed" />
          <div className="grid grid-cols-2 gap-4 border-t border-line pt-4">
            <Figure label="False positives" value={s.benign_blocked} note={`benign actions blocked in ${s.benign_scenarios} controls`} />
            <Figure label="Added time per call" value={latency(s.median_overhead_ms)} note={mode === "supervisor" ? "median wait for the reviewing model" : "median, excluding the action itself"} />
          </div>
          {s.holds_auto_rejected > 0 && (
            <p className="flex items-start gap-2 rounded-lg bg-raised px-3 py-2 text-[12.5px] text-ink-2">
              <Clock className="mt-0.5 size-3.5 shrink-0 text-warn" />
              {s.holds_auto_rejected} hold(s) were auto-rejected so the run did not wait for a human. They are counted here, not as denials.
            </p>
          )}
          {(s.excluded ?? 0) > 0 && (
            <p className="flex items-start gap-2 rounded-lg border border-warn/30 bg-warn-soft px-3 py-2 text-[12.5px]">
              <TriangleAlert className="mt-0.5 size-3.5 shrink-0 text-warn" />
              {s.excluded} run(s) are left out of these figures: something failed (the reviewing model, the agent or a timeout) before any effect was seen, and an outage is not a verdict.
            </p>
          )}
          {s.errors - (s.excluded ?? 0) > 0 && (
            <p className="flex items-start gap-2 rounded-lg border border-bad/30 bg-bad-soft px-3 py-2 text-[12.5px]">
              <TriangleAlert className="mt-0.5 size-3.5 shrink-0 text-bad" />
              {s.errors - (s.excluded ?? 0)} scenario run(s) ended with an error or a timeout. See the table below.
            </p>
          )}
        </div>
      )}
    </Card>
  );
}

export default function ScoreboardPage() {
  const toast = useToast();
  const { status } = useLive();
  const [picked, setPicked] = useState<string>("");
  const latest = usePoll<{ run: ScoreRun | null }>("/v1/scoreboard/latest", 3000);
  const history = usePoll<{ runs: RunInfo[] }>("/v1/scoreboard/runs", 20000);
  const chosen = usePoll<{ run: ScoreRun | null }>(picked ? `/v1/scoreboard/runs/${picked}` : null, 0);
  const data = picked ? chosen.data : latest.data;
  const refresh = latest.refresh;
  const [agent, setAgent] = useState<"replay" | "live">("replay");
  const [scope, setScope] = useState<"quick" | "all">("all");
  const [baseline, setBaseline] = useState<"off" | "supervisor">("off");
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useLiveEvent((ev) => {
    if (ev.type !== "scoreboard") return;
    refresh();
    if (ev.data?.status === "done") {
      history.refresh();
      toast({ tone: "ok", title: "Scoreboard run finished", body: `${ev.data.total} scenario runs measured` });
    }
  });

  const run = data?.run ?? null;
  const running = latest.data?.run?.status === "running";
  const canLive = !!status?.gemini_configured;
  // Live controls stay enabled while the server key looks broken (its status is
  // only refreshed by using it), but the prompt to add your own key shows.
  const keyTrouble = !canLive || (status?.llm?.source === "server" && brokenServerKey(status.llm.server));
  const describe = (r: RunInfo) =>
    `${dateTime(r.finished_at)} · ${r.agent_kind === "live" ? "live" : "scripted"} agent · ${r.modes.map((m) => MODES[m]?.short ?? m).join(" / ")} · ${r.scenarios} scenarios`;
  const modes: Mode[] = run ? MODE_ORDER.filter((m) => (run.modes ?? ["off", "on"]).includes(m)) : ["off", "on"];

  const start = async () => {
    setStarting(true);
    setError(null);
    try {
      await api("/v1/scoreboard/runs", { agent, quick: scope === "quick", supervisor: baseline === "supervisor" });
      setPicked("");
      await refresh();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setStarting(false);
    }
  };

  const rows = new Map<string, { any: ScoreResult } & Partial<Record<Mode, ScoreResult>>>();
  for (const r of run?.results ?? []) {
    const key = `${r.scenario}-${r.variant}`;
    const row = rows.get(key) ?? { any: r };
    row[r.mode] = r;
    rows.set(key, row);
  }
  const attackRuns = (mode: Mode) => (run?.results ?? []).filter((r) => r.mode === mode && r.kind === "attack");
  const effectCount = (mode: Mode, key: string) => attackRuns(mode).filter((r) => r.unauthorized.includes(key)).length;
  const effectTotal = (mode: Mode, scenarios: string[]) => attackRuns(mode).filter((r) => scenarios.includes(r.scenario)).length;
  const used = modelsUsed(run?.models_used);

  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow="PACT-Bench"
        title="Scoreboard"
        description="The same attacks against the same agent under different protections. Same code, tools, credentials, repository and scenarios: only what stands between the agent and the targets changes. Every number is read from the targets themselves."
      />

      <Card>
        <div className="flex flex-wrap items-center gap-x-6 gap-y-3 px-5 py-4">
          <div className="flex items-center gap-3">
            <span className="text-[13px] font-medium">Agent</span>
            <Segmented
              label="Agent"
              value={agent}
              onChange={setAgent}
              options={[
                { value: "replay", label: "Scripted" },
                { value: "live", label: <><Sparkles className="size-3.5" /> Live</>, disabled: !canLive, title: canLive ? `${status?.model} tool-calling loop` : "Needs a working Gemini key" },
              ]}
            />
          </div>
          <div className="flex items-center gap-3">
            <span className="text-[13px] font-medium">Phrasings</span>
            <Segmented label="Phrasings" value={scope} onChange={setScope} options={[{ value: "quick", label: "One per attack" }, { value: "all", label: "All phrasings" }]} />
          </div>
          <div className="flex items-center gap-3">
            <span className="text-[13px] font-medium">Compare against</span>
            <Segmented
              label="Baselines"
              value={baseline}
              onChange={setBaseline}
              options={[
                { value: "off", label: "Unprotected" },
                { value: "supervisor", label: "+ AI supervisor", disabled: !canLive, title: canLive ? "A second model call reviews every tool call" : "Needs a working Gemini key" },
              ]}
            />
          </div>
          <button className="btn btn-primary ml-auto" disabled={running || starting} onClick={start}>
            {running || starting ? <Spinner /> : <Play className="size-4" />}
            {running ? "Running…" : "Run again"}
          </button>
        </div>
        {keyTrouble && (
          <p className="border-t border-line px-5 py-2.5 text-[12.5px] text-muted">
            The live agent and the AI-supervisor comparison need a working Gemini key. <UseOwnKeyButton className="text-[12.5px]" />
          </p>
        )}
        {error && <p className="border-t border-line bg-bad-soft px-5 py-2.5 text-[13px]">{error}</p>}
        <div className="border-t border-line bg-raised px-5 py-3">
          {run ? (
            <>
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <p className="text-[13.5px] font-semibold">
                  {run.status === "running" ? `Running: ${run.done} of ${run.total} scenario runs finished` : `Measured on our ${run.scenarios} seed scenarios at ${dateTime(run.finished_at ?? run.started_at)}`}
                </p>
                {(history.data?.runs.length ?? 0) > 1 ? (
                  <select className="input !h-8 !w-auto max-w-full !py-0 text-[12.5px]" value={picked} onChange={(e) => setPicked(e.target.value)} aria-label="Choose a run">
                    <option value="">Latest run</option>
                    {history.data!.runs.map((r) => (
                      <option key={r.run_id} value={r.run_id}>
                        {describe(r)}
                      </option>
                    ))}
                  </select>
                ) : (
                  <span className="code text-[11.5px] text-muted">{run.run_id}</span>
                )}
              </div>
              <p className="mt-0.5 text-[12.5px] text-muted">
                {run.agent_kind === "live" ? `Live ${run.model} tool-calling loop` : "Scripted agent: fixed tool calls, so this shows enforcement, not how often a model follows an injection"} ·{" "}
                {run.quick ? "one phrasing per attack" : "every phrasing of each attack"}. These numbers describe these scenarios only, not a general security rate.
              </p>
              {used && (
                <p className="mt-0.5 text-[12.5px] text-muted">
                  Model calls answered by: <span className="code text-ink-2">{used}</span>
                  {run.key_source && <>, billed to {billedTo(run.key_source)}</>}
                </p>
              )}
              {run.status === "running" && (
                <div className="mt-2.5 h-1.5 overflow-hidden rounded-full bg-line">
                  <div className="h-full rounded-full bg-accent transition-[width] duration-500" style={{ width: `${run.total ? (100 * run.done) / run.total : 0}%` }} />
                </div>
              )}
            </>
          ) : (
            <p className="text-[13px] text-muted">No run yet. A scripted run of all phrasings takes under a minute.</p>
          )}
        </div>
      </Card>

      {!data ? (
        <div className="grid gap-6 lg:grid-cols-2">
          <Skeleton className="h-[380px] rounded-[14px]" />
          <Skeleton className="h-[380px] rounded-[14px]" />
        </div>
      ) : !run ? (
        <Card>
          <Empty art={<ScoreArt />} title="Measure PACT against the same attacks" action={<button className="btn btn-primary" onClick={start} disabled={starting}><Play className="size-4" /> Run the scoreboard</button>}>
            Each attack scenario runs once per configuration, and the targets report what actually happened.
          </Empty>
        </Card>
      ) : (
        <>
          <div className={`grid gap-6 ${modes.length === 3 ? "xl:grid-cols-3" : "lg:grid-cols-2"}`}>
            {modes.map((m) => (
              <Column key={m} mode={m} s={run.summary[m]} />
            ))}
          </div>

          <Card>
            <CardHeader icon={FlaskConical} title="Effects by attack type" subtitle="Scenario runs where the attack’s goal showed up on a target" />
            <div className="grid gap-x-10 gap-y-5 p-5 md:grid-cols-2">
              {EFFECTS.map((e) => (
                <div key={e.key} className="space-y-2.5">
                  <p className="text-[13.5px] font-semibold">{e.label}</p>
                  {modes.map((m) => (
                    <Meter key={m} label={MODES[m].title} value={effectCount(m, e.key)} total={effectTotal(m, e.scenarios)} tone="bad" />
                  ))}
                </div>
              ))}
            </div>
          </Card>

          {rows.size > 0 && (
            <Card className="overflow-hidden">
              <CardHeader icon={Info} title="Every scenario, every configuration" subtitle="Hover an effect for the oracle check behind it. PACT’s failures are shown exactly like its successes." />
              <div className="overflow-x-auto">
                <table className="w-full text-left text-[13px]">
                  <thead className="bg-raised text-[12px] text-muted">
                    <tr className="border-b border-line">
                      <th className="px-5 py-2 font-medium">Scenario</th>
                      {modes.map((m) => (
                        <th key={m} className="px-3 py-2 font-medium">{MODES[m].title}</th>
                      ))}
                      <th className="px-3 py-2 font-medium">PACT decisions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {[...rows.entries()].map(([key, row]) => (
                      <tr key={key} className="border-b border-line align-top last:border-0">
                        <td className="px-5 py-3">
                          <div className="flex items-center gap-2">
                            <span className="code text-muted">
                              {row.any.scenario}
                              {row.any.label ? `·${row.any.variant.toUpperCase()}` : ""}
                            </span>
                            <span className={`text-[11px] font-medium ${row.any.kind === "attack" ? "text-bad" : "text-muted"}`}>{row.any.kind === "attack" ? "attack" : "control"}</span>
                          </div>
                          <p className="font-medium">{row.any.title}</p>
                          {row.any.label && <p className="text-[12px] text-muted">Phrasing: {row.any.label}</p>}
                        </td>
                        {modes.map((m) => (
                          <td key={m} className="px-3 py-3">
                            <Outcome r={row[m]} />
                            {m === "supervisor" &&
                              (row[m]?.blocks ?? []).map((b, i) => (
                                <span key={i} className="mt-1 block max-w-[340px] text-[12px] text-muted" title={b.reason}>
                                  Blocked <span className="code text-ink-2">{b.tool}</span>: <span className="line-clamp-2 inline">{b.reason}</span>
                                </span>
                              ))}
                          </td>
                        ))}
                        <td className="px-3 py-3">
                          {row.on ? (
                            <div className="flex flex-wrap gap-1.5">
                              <StatusBadge tone="ok">{row.on.allowed}</StatusBadge>
                              {row.on.denied > 0 && <StatusBadge tone="bad">{row.on.denied}</StatusBadge>}
                              {row.on.held > 0 && <StatusBadge tone="warn">{row.on.held}</StatusBadge>}
                            </div>
                          ) : (
                            <span className="text-muted">—</span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="flex flex-wrap gap-x-5 gap-y-1 border-t border-line bg-raised px-5 py-2.5 text-[12px] text-muted">
                <span className="inline-flex items-center gap-1.5"><CircleCheck className="size-3.5 text-ok" /> allowed</span>
                <span className="inline-flex items-center gap-1.5"><CircleX className="size-3.5 text-bad" /> denied</span>
                <span className="inline-flex items-center gap-1.5"><CirclePause className="size-3.5 text-warn" /> held (auto-rejected)</span>
              </div>
            </Card>
          )}
        </>
      )}
    </div>
  );
}
