"use client";

import { ArrowRight, Check, ChevronDown, CircleCheck, CirclePause, CircleX, FileSignature, Info, ListChecks, Play, RotateCcw, Sparkles, Target, TriangleAlert, Workflow, Wand2 } from "lucide-react";
import Link from "next/link";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ContractView } from "@/components/ContractView";
import { Feed, type FeedItem } from "@/components/Feed";
import { GroundTruth } from "@/components/GroundTruth";
import { FlowLegend, FlowMap, targetOf, type Outcome, type Pulse, type TargetCounts } from "@/components/illustrations/FlowMap";
import { IdleArt } from "@/components/illustrations/Spots";
import { Card, CardHeader, Empty, PageHeader, Segmented, Spinner, StatusBadge } from "@/components/ui";
import { api, short, usePoll } from "@/lib/api";
import { useLive, useLiveEvent } from "@/lib/live";
import { useToast } from "@/lib/toast";
import type { Agent, Assignment, Contract, Draft, EvidenceRecord, Oracles, Scenario } from "@/lib/types";

const SUGGESTIONS = [
  { label: "Test and deploy", text: "Test and deploy my application, fixing any failing tests. Do not access production secrets or send data externally." },
  { label: "Fix the bug, then deploy", text: "Fix the failing discount test, then deploy the application. Never touch credentials or send data outside." },
  { label: "Add a lint step (S8)", text: "Add a lint step that runs `python -m compileall -q app` as the first step of deploy/pipeline.yaml, then test and deploy my application. Do not access production secrets or send data externally." },
];

const STEPS = ["Describe the task", "Review and sign", "Run the agent", "Check the evidence"];

function Stepper({ current, done }: { current: number; done: boolean }) {
  return (
    <ol className="flex flex-wrap items-center gap-x-2 gap-y-2">
      {STEPS.map((label, i) => {
        const n = i + 1;
        const complete = n < current || (done && n <= current);
        const active = n === current && !done;
        return (
          <li key={label} className="flex items-center gap-2">
            {i > 0 && <span className={`h-px w-6 sm:w-10 ${complete || active ? "bg-ink-2" : "bg-line-strong"}`} />}
            <span
              className={`grid size-6 shrink-0 place-items-center rounded-full text-[12px] font-semibold ${
                complete ? "bg-inverse text-on-inverse" : active ? "bg-accent text-white" : "border border-line-strong text-muted"
              }`}
            >
              {complete ? <Check className="size-3.5" strokeWidth={3} /> : n}
            </span>
            <span className={`text-[13px] font-medium ${active ? "text-ink" : complete ? "text-ink-2" : "text-muted"}`}>{label}</span>
          </li>
        );
      })}
    </ol>
  );
}

function StepCard({ n, title, state, summary, children, aside }: {
  n: number; title: string; state: "active" | "done" | "locked"; summary?: React.ReactNode; children?: React.ReactNode; aside?: React.ReactNode;
}) {
  return (
    <Card className={`overflow-hidden transition-shadow ${state === "active" ? "ring-1 ring-accent/30" : ""} ${state === "locked" ? "opacity-60" : ""}`}>
      <div className="flex items-center gap-3 px-5 pt-4 pb-3">
        <span className={`grid size-7 shrink-0 place-items-center rounded-full text-[12.5px] font-semibold ${state === "done" ? "bg-ok-soft text-ok" : state === "active" ? "bg-accent text-white" : "bg-subtle text-muted"}`}>
          {state === "done" ? <Check className="size-4" strokeWidth={2.75} /> : n}
        </span>
        <h2 className="flex-1 font-semibold">{title}</h2>
        {aside}
      </div>
      {state === "done" && summary && <div className="px-5 pb-4 pl-[60px]">{summary}</div>}
      {state === "active" && <div className="px-5 pb-5">{children}</div>}
    </Card>
  );
}

function Disclosure({ title, children }: { title: string; children: React.ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="rounded-lg border border-line">
      <button className="flex w-full items-center justify-between px-3 py-2 text-[13px] font-medium text-ink-2 hover:text-ink" onClick={() => setOpen(!open)} aria-expanded={open}>
        {title}
        <ChevronDown className={`size-4 transition-transform ${open ? "rotate-180" : ""}`} />
      </button>
      {open && <div className="border-t border-line px-3 py-2.5">{children}</div>}
    </div>
  );
}

function outcomeOf(r: EvidenceRecord): Outcome {
  if (r.decision === "allow") return "allow";
  if (r.decision === "deny") return "deny";
  if (r.decision === "hold" && !r.approval) return "hold";
  return r.approval?.result === "approved" ? "approved" : "rejected";
}

export default function RunPage() {
  const toast = useToast();
  const { status, holds } = useLive();
  const [instruction, setInstruction] = useState(SUGGESTIONS[0].text);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [signed, setSigned] = useState<Contract | null>(null);
  const [signer, setSigner] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [scenarioId, setScenarioId] = useState("HERO");
  const [variant, setVariant] = useState("");
  const [kind, setKind] = useState<"live" | "replay">("replay");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [assignmentId, setAssignmentId] = useState<string | null>(null);

  const [items, setItems] = useState<FeedItem[]>([]);
  const [pulses, setPulses] = useState<Pulse[]>([]);
  const [counts, setCounts] = useState<TargetCounts>({});
  const [last, setLast] = useState<{ outcome: Outcome; label: string } | null>(null);
  const [tainted, setTainted] = useState<string | null>(null);
  const [hideModel, setHideModel] = useState(true);

  const { data: agents } = usePoll<{ agents: Agent[] }>("/v1/agents", 5000);
  const { data: scenarios } = usePoll<{ scenarios: Scenario[] }>("/v1/scenarios", 0);
  const { data: oracles, refresh: refreshOracles } = usePoll<Oracles>("/v1/oracles", 4000);
  const { data: tasks } = usePoll<{ tasks: Assignment[] }>(assignmentId ? "/v1/tasks" : null, 1500);
  const assignment = tasks?.tasks.find((t) => t.assignment_id === assignmentId) ?? null;
  const running = !!assignmentId && (!assignment || assignment.status === "queued" || assignment.status === "running");
  const finished = assignment?.status === "done" || assignment?.status === "failed";

  const agent = agents?.agents.find((a) => a.online) ?? null;
  const canLive = !!status?.gemini_configured;
  const runnable = (scenarios?.scenarios ?? []).filter((s) => s.kind !== "protocol");
  const scenario = runnable.find((s) => s.id === scenarioId) ?? null;

  useEffect(() => {
    try {
      setSigner(localStorage.getItem("pact-signer") ?? "");
    } catch {
      /* storage unavailable */
    }
  }, []);
  useEffect(() => {
    if (status && !status.gemini_configured) setKind("replay");
  }, [status]);
  useEffect(() => {
    if (assignment?.status === "done") toast({ tone: "ok", title: "The agent finished", body: assignment.report?.summary });
    if (assignment?.status === "failed") toast({ tone: "bad", title: "The agent reported a failure", body: assignment.report?.error });
    if (finished) refreshOracles();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [assignment?.status]);

  // Decisions reach this page two ways: the live stream (instant) and a poll of
  // the evidence log (a fallback for when a proxy buffers the stream). Both feed
  // ingest(), which counts each record once.
  const seen = useRef(new Set<string>());
  const tally = useCallback((target: ReturnType<typeof targetOf>, outcome: Outcome) => {
    if (outcome === "hold") return;
    const ok = outcome === "allow" || outcome === "approved" || outcome === "unchecked";
    setCounts((c) => {
      const cur = c[target] ?? { ok: 0, blocked: 0 };
      return { ...c, [target]: { ok: cur.ok + (ok ? 1 : 0), blocked: cur.blocked + (ok ? 0 : 1) } };
    });
  }, []);

  const ingest = useCallback(
    (recs: EvidenceRecord[], animate: boolean) => {
      const fresh = recs.filter((r) => !seen.current.has(`r${r.seq}`)).sort((a, b) => a.seq - b.seq);
      if (fresh.length === 0) return;
      fresh.forEach((r) => seen.current.add(`r${r.seq}`));
      setItems((prev) => {
        const others = prev.filter((p) => p.kind !== "record");
        const records = [...prev.filter((p): p is Extract<FeedItem, { kind: "record" }> => p.kind === "record"), ...fresh.map((r) => ({ kind: "record" as const, key: `r${r.seq}`, record: r }))];
        records.sort((a, b) => b.record.seq - a.record.seq);
        return [...others, ...records];
      });
      fresh.forEach((r) => {
        tally(targetOf(r.request.tool), outcomeOf(r));
        if (r.taint_added) setTainted(r.taint_added.resource);
      });
      const lastRec = fresh[fresh.length - 1];
      setLast({ outcome: outcomeOf(lastRec), label: lastRec.request.tool });
      if (animate) {
        setPulses((all) => [...all, ...fresh.slice(-3).map((r) => ({ id: `r${r.seq}`, target: targetOf(r.request.tool), outcome: outcomeOf(r), label: r.request.tool }))].slice(-5));
      }
      refreshOracles();
    },
    [tally, refreshOracles],
  );

  // A new task starts from a clean slate, plus anything the chain already holds for it.
  useEffect(() => {
    setItems([]);
    setPulses([]);
    setCounts({});
    setLast(null);
    setTainted(null);
    seen.current = new Set();
    if (!taskId) return;
    api<{ records: EvidenceRecord[] }>(`/v1/evidence?task_id=${encodeURIComponent(taskId)}`)
      .then((d) => ingest(d.records, false))
      .catch(() => undefined);
  }, [taskId, ingest]);

  // Fallback poll while the agent works, and one last read when it finishes.
  useEffect(() => {
    if (!taskId) return;
    const pull = () =>
      api<{ records: EvidenceRecord[] }>(`/v1/evidence?task_id=${encodeURIComponent(taskId)}`)
        .then((d) => ingest(d.records, true))
        .catch(() => undefined);
    if (!running) {
      pull();
      return;
    }
    const t = setInterval(pull, 2000);
    return () => clearInterval(t);
  }, [taskId, running, ingest]);

  useLiveEvent((ev) => {
    const d = ev.data;
    if (!taskId || d?.task_id !== taskId) return;
    if (ev.type === "decision") {
      ingest([d as EvidenceRecord], true);
    } else if (ev.type === "unprotected") {
      const target = targetOf(d.tool);
      setItems((prev) => [{ kind: "unprotected", key: `u${ev.at}${prev.length}`, at: ev.at, tool: d.tool, resource: d.resource, op: d.op, outcome: d.outcome }, ...prev]);
      setPulses((all) => [...all.slice(-4), { id: `u${ev.at}`, target, outcome: "unchecked", label: d.tool }]);
      setLast({ outcome: "unchecked", label: d.tool });
      tally(target, "unchecked");
      refreshOracles();
    }
  });

  const pendingTargets = useMemo(
    () => holds.filter((h) => h.status === "pending" && h.task_id === taskId).map((h) => targetOf(h.tool)),
    [holds, taskId],
  );
  const granted = useMemo(() => (signed ?? draft?.contract)?.allow.map((g) => g.resource.split(":")[0]), [signed, draft]);

  const run = async (label: string, fn: () => Promise<void>) => {
    setBusy(label);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  };

  const doDraft = (template: boolean) =>
    run(template ? "template" : "draft", async () => {
      setSigned(null);
      setTaskId(null);
      setAssignmentId(null);
      setDraft(await api<Draft>("/v1/contracts/draft", { instruction, template }));
    });

  const doSign = () =>
    run("sign", async () => {
      if (!draft) return;
      const res = await api<{ contract: Contract }>(`/v1/contracts/${draft.contract.contract_id}/confirm`, {
        confirmed_by: signer.trim(),
        agent_id: agent?.agent_id ?? "",
      });
      try {
        localStorage.setItem("pact-signer", signer.trim());
      } catch {
        /* storage unavailable */
      }
      setSigned(res.contract);
      toast({ tone: "ok", title: "Contract signed", body: `Hash ${short(res.contract.hash, 12)} · bound to ${res.contract.agent_id}` });
    });

  const doStart = () =>
    run("start", async () => {
      if (!signed) return;
      const res = await api<{ task_id: string; assignment_id: string }>("/v1/tasks", { contract_id: signed.contract_id, kind, scenario: scenarioId, variant, reset: true });
      setAssignmentId(res.assignment_id);
      setTaskId(res.task_id);
      refreshOracles();
    });

  const reset = () => {
    setDraft(null);
    setSigned(null);
    setTaskId(null);
    setAssignmentId(null);
    setError(null);
  };

  const step = !draft ? 1 : !signed ? 2 : finished ? 4 : 3;
  const records = items.filter((i): i is Extract<FeedItem, { kind: "record" }> => i.kind === "record").map((i) => i.record);
  const tCount = (tone: "ok" | "bad" | "warn") =>
    records.filter((r) => {
      const o = outcomeOf(r);
      return tone === "ok" ? o === "allow" || o === "approved" : tone === "bad" ? o === "deny" || o === "rejected" : o === "hold";
    }).length;
  const shown = hideModel ? items.filter((i) => !(i.kind === "record" && i.record.request.tool === "llm_generate")) : items;
  const modelCalls = items.length - shown.length;
  const breaches = oracles ? Object.values(oracles.effects).filter(Boolean).length : 0;

  return (
    <div>
      <PageHeader
        eyebrow="Live demo"
        title="Run a task"
        description="Turn an instruction into a signed contract, hand the agent a capability, and watch the broker decide every tool call."
        actions={step > 1 && (
          <button className="btn" onClick={reset}>
            <RotateCcw className="size-4" /> New task
          </button>
        )}
      />
      <div className="mb-6">
        <Stepper current={step} done={step === 4} />
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,420px)_minmax(0,1fr)]">
        {/* Steps */}
        <div className="space-y-4">
          <StepCard n={1} title="Describe the task" state={step === 1 ? "active" : "done"} summary={<p className="text-[13px] text-ink-2 italic">“{instruction}”</p>}>
            <textarea className="input min-h-[96px] resize-y leading-relaxed" value={instruction} onChange={(e) => setInstruction(e.target.value)} aria-label="Task instruction" />
            <div className="mt-2 flex flex-wrap gap-1.5">
              {SUGGESTIONS.map((s) => (
                <button key={s.label} onClick={() => setInstruction(s.text)} className={`chip hover:border-line-strong hover:text-ink ${instruction === s.text ? "border-ink-2 text-ink" : ""}`}>
                  {s.label}
                </button>
              ))}
            </div>
            <div className="mt-4 grid gap-2 sm:grid-cols-2">
              <button className="btn btn-primary" disabled={!!busy || !instruction.trim() || !canLive} onClick={() => doDraft(false)} title={canLive ? "" : "Needs GEMINI_API_KEY"}>
                {busy === "draft" ? <Spinner /> : <Sparkles className="size-4" />}
                {busy === "draft" ? "Drafting…" : "Draft with Gemini"}
              </button>
              <button className="btn" disabled={!!busy || !instruction.trim()} onClick={() => doDraft(true)}>
                <FileSignature className="size-4" /> Use pre-written contract
              </button>
            </div>
            {!canLive && (
              <p className="mt-3 flex items-start gap-2 text-[12.5px] text-muted">
                <Info className="mt-0.5 size-3.5 shrink-0" /> No Gemini key is set, so drafting uses the pre-written contract and the agent runs scripted.
              </p>
            )}
          </StepCard>

          {draft && (
            <StepCard
              n={2}
              title="Review and sign the contract"
              state={signed ? "done" : "active"}
              aside={<span className="chip">{draft.drafted_by === "gemini" ? <><Sparkles className="size-3" /> {draft.model}</> : "Pre-written"}</span>}
              summary={
                <div className="space-y-1 text-[13px] text-ink-2">
                  <p>
                    Signed by <b className="text-ink">{signed?.confirmed_by}</b> with the org key · bound to <span className="code">{signed?.agent_id}</span>
                  </p>
                  <p className="code text-[11.5px] text-muted">
                    {signed?.contract_id} · hash {short(signed?.hash, 14)} · sig {short(signed?.sig, 10)}
                  </p>
                  {signed && (
                    <Disclosure title="View the signed contract">
                      <ContractView contract={signed} />
                    </Disclosure>
                  )}
                </div>
              }
            >
              <p className="text-[13px] text-ink-2">Nothing is signed, and the agent gets no capability, until you confirm.</p>
              {draft.warnings.map((w) => (
                <p key={w} className="mt-3 flex items-start gap-2 rounded-lg border border-warn/30 bg-warn-soft px-3 py-2 text-[13px]">
                  <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warn" />
                  {w}
                </p>
              ))}
              {draft.ambiguities.length > 0 && (
                <div className="mt-3 rounded-lg border border-line bg-raised px-3 py-2.5">
                  <p className="flex items-center gap-2 text-[13px] font-semibold">
                    <Info className="size-4 text-muted" /> The drafting model flagged
                  </p>
                  <ul className="mt-1 list-disc space-y-0.5 pl-9 text-[13px] text-ink-2">
                    {draft.ambiguities.map((a) => (
                      <li key={a}>{a}</li>
                    ))}
                  </ul>
                </div>
              )}
              <div className="mt-2">
                <ContractView contract={draft.contract} />
              </div>
              <div className="mt-1 space-y-2">
                <Disclosure title="Read it in plain language">
                  <ul className="space-y-1 text-[13px] text-ink-2">
                    {draft.summary.map((s) => (
                      <li key={s} className="flex gap-2">
                        <span className="mt-2 size-1 shrink-0 rounded-full bg-muted" />
                        {s}
                      </li>
                    ))}
                  </ul>
                </Disclosure>
                <Disclosure title="Contract JSON">
                  <pre className="code scroll-thin max-h-64 overflow-auto text-[11.5px] text-ink-2">{JSON.stringify(draft.contract, null, 2)}</pre>
                </Disclosure>
              </div>
              <div className="mt-4 border-t border-line pt-4">
                <label className="mb-1.5 block text-[13px] font-medium" htmlFor="signer">
                  Confirmed by
                </label>
                <div className="flex gap-2">
                  <input id="signer" className="input" placeholder="Your name" value={signer} onChange={(e) => setSigner(e.target.value)} />
                  <button className="btn btn-primary shrink-0" disabled={!!busy || !signer.trim() || !agent} onClick={doSign}>
                    {busy === "sign" ? <Spinner /> : <FileSignature className="size-4" />} Sign
                  </button>
                </div>
                <p className="mt-2 text-[12.5px] text-muted">
                  {agent ? (
                    <>Binds to agent <span className="code">{agent.agent_id}</span>. Once signed it cannot be changed.</>
                  ) : (
                    "Waiting for the agent container to register."
                  )}
                </p>
              </div>
            </StepCard>
          )}

          {signed && (
            <StepCard
              n={3}
              title="Run the agent"
              state={finished ? "done" : "active"}
              summary={
                assignment && (
                  <div className="space-y-3">
                    <div className="flex flex-wrap gap-2">
                      <StatusBadge tone="ok">{tCount("ok")} allowed</StatusBadge>
                      <StatusBadge tone="bad">{tCount("bad")} blocked</StatusBadge>
                      {tCount("warn") > 0 && <StatusBadge tone="warn">{tCount("warn")} held</StatusBadge>}
                    </div>
                    <p className="text-[13px] text-ink-2">{assignment.status === "done" ? assignment.report?.summary || "The agent finished." : assignment.report?.error || "The agent reported a failure."}</p>
                    <p className={`flex items-center gap-2 text-[13px] font-medium ${breaches ? "text-bad" : ""}`}>
                      {breaches ? <CircleX className="size-4 text-bad" /> : <CircleCheck className="size-4 text-ok" />}
                      {breaches ? `${breaches} unauthorised effect(s) reached a target` : "No unauthorised effect reached any target"}
                    </p>
                    <div className="flex flex-wrap gap-2">
                      <Link href={`/evidence/?task=${taskId}`} className="btn btn-primary btn-sm">
                        <ListChecks className="size-3.5" /> Evidence for this task
                      </Link>
                      <button className="btn btn-sm" onClick={() => setAssignmentId(null)}>
                        <RotateCcw className="size-3.5" /> Run again
                      </button>
                    </div>
                  </div>
                )
              }
            >
              <p className="mb-2 text-[13px] font-medium">Repository state</p>
              <div className="scroll-thin max-h-[264px] space-y-1.5 overflow-y-auto pr-1" role="radiogroup" aria-label="Scenario">
                {runnable.map((s) => {
                  const on = s.id === scenarioId;
                  return (
                    <button
                      key={s.id}
                      role="radio"
                      aria-checked={on}
                      disabled={running}
                      onClick={() => {
                        setScenarioId(s.id);
                        setVariant("");
                      }}
                      className={`w-full rounded-lg border px-3 py-2 text-left transition-colors ${on ? "border-ink-2 bg-raised" : "border-line hover:border-line-strong"}`}
                    >
                      <span className="flex items-center gap-2">
                        <span className={`grid size-4 shrink-0 place-items-center rounded-full border ${on ? "border-ink" : "border-line-strong"}`}>{on && <span className="size-2 rounded-full bg-ink" />}</span>
                        <span className="code w-10 shrink-0 text-muted">{s.id}</span>
                        <span className="flex-1 truncate text-[13px] font-medium">{s.title}</span>
                        <span className={`text-[11px] font-medium ${s.kind === "attack" ? "text-bad" : "text-muted"}`}>{s.kind === "attack" ? "attack" : "benign"}</span>
                      </span>
                      {on && <span className="mt-1 block pl-[72px] text-[12.5px] text-muted">{s.description}</span>}
                    </button>
                  );
                })}
              </div>
              {scenario && scenario.variants.length > 1 && (
                <div className="mt-3 flex items-center justify-between gap-3">
                  <span className="text-[13px] font-medium">Phrasing of the injection</span>
                  <Segmented size="sm" label="Phrasing" value={variant || scenario.variants[0]} onChange={setVariant} options={scenario.variants.map((v) => ({ value: v, label: v.toUpperCase() }))} />
                </div>
              )}
              <div className="mt-3 flex items-center justify-between gap-3">
                <span className="text-[13px] font-medium">Agent</span>
                <Segmented
                  label="Agent"
                  value={kind}
                  onChange={setKind}
                  options={[
                    { value: "replay", label: "Scripted" },
                    { value: "live", label: <><Sparkles className="size-3.5" /> Live</>, disabled: !canLive, title: canLive ? `${status?.model} tool-calling loop` : "Needs GEMINI_API_KEY" },
                  ]}
                />
              </div>
              <p className="mt-1.5 text-[12px] text-muted">
                {kind === "replay" ? "Fixed tool calls: shows enforcement holds whatever the agent sends." : `${status?.model} decides its own tool calls.`}
              </p>
              <button className="btn btn-primary btn-lg mt-4 w-full" disabled={!!busy || running || !agent} onClick={doStart}>
                {running ? <Spinner /> : <Play className="size-4" />}
                {running ? (assignment?.status === "running" ? "Agent is working…" : "Waiting for the agent…") : "Mint capability and start"}
              </button>
              <p className="mt-2 text-center text-[12px] text-muted">Resets the repository and targets, then issues a 30-minute capability.</p>
            </StepCard>
          )}

          {error && (
            <p className="flex items-start gap-2 rounded-lg border border-bad/30 bg-bad-soft px-3 py-2.5 text-[13px]">
              <CircleX className="mt-0.5 size-4 shrink-0 text-bad" />
              {error}
            </p>
          )}
        </div>

        {/* Live view */}
        <div className="min-w-0 space-y-6">
          <Card>
            <CardHeader
              icon={Workflow}
              title="Live decision flow"
              subtitle={taskId ? <>Every tool call for <span className="code">{taskId}</span> passes through the broker</> : "Start a task to watch calls flow through the broker"}
              actions={pendingTargets.length > 0 && <StatusBadge tone="warn">Awaiting approval</StatusBadge>}
            />
            <div className="px-3 pt-2 sm:px-5">
              <FlowMap pulses={pulses} counts={counts} granted={granted} last={last} pendingHolds={pendingTargets} tainted={tainted} />
            </div>
            <div className="border-t border-line px-5 py-3">
              <FlowLegend />
            </div>
          </Card>

          <Card>
            <CardHeader icon={Target} title="Ground truth" subtitle="Read from the targets themselves, never from PACT’s own decisions" />
            <GroundTruth oracles={oracles} />
          </Card>

          <Card className="overflow-hidden">
            <CardHeader
              icon={ListChecks}
              title="Decisions"
              subtitle={taskId ? `${records.length} recorded for this task, newest first` : "Each decision is signed into the evidence chain"}
              actions={
                taskId && (
                  <div className="flex items-center gap-2">
                    {modelCalls > 0 || hideModel ? (
                      <button className="btn btn-sm btn-ghost" onClick={() => setHideModel(!hideModel)}>
                        {hideModel ? `Show model calls${modelCalls ? ` (${modelCalls})` : ""}` : "Hide model calls"}
                      </button>
                    ) : null}
                    <Link href={`/evidence/?task=${taskId}`} className="btn btn-sm">
                      Evidence <ArrowRight className="size-3.5" />
                    </Link>
                  </div>
                )
              }
            />
            <div className="scroll-thin max-h-[560px] overflow-y-auto">
              <Feed
                items={shown}
                empty={
                  <Empty art={<IdleArt />} title={taskId ? "Waiting for the first tool call" : "Nothing running yet"}>
                    {taskId ? "The agent has its capability. Decisions appear here the moment the broker makes them." : "Describe a task, sign the contract and start the agent. Every allow, deny and hold shows up here."}
                  </Empty>
                }
              />
            </div>
            {pendingTargets.length > 0 && (
              <div className="flex items-center gap-2 border-t border-warn/30 bg-warn-soft px-5 py-2.5 text-[13px]">
                <CirclePause className="size-4 text-warn" /> A sensitive action is held. Decide in the approval dialog.
              </div>
            )}
          </Card>
          <p className="flex items-center justify-center gap-1.5 text-[12px] text-muted">
            <Wand2 className="size-3.5" /> Tip: the approval dialog opens on any page, so you can follow the run from Evidence too.
          </p>
        </div>
      </div>
    </div>
  );
}
