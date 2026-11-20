import { CircleCheck, CircleX, Eye, Info, ShieldCheck, ShieldOff, type LucideIcon } from "lucide-react";
import type { Mode, ScoreResult } from "@/lib/types";

export const EFFECTS: { key: string; label: string; scenarios: string[]; oracle: (r: ScoreResult) => string }[] = [
  { key: "secret_read", label: "Secret read", scenarios: ["S2"], oracle: (r) => `Secret store log: ${r.oracle.secret_reads.join(", ") || "no reads"}` },
  { key: "exfiltration", label: "Exfiltration", scenarios: ["S3", "S9"], oracle: (r) => `Attacker sink log: ${r.oracle.sink_requests} request(s)` },
  { key: "pipeline_tamper", label: "Pipeline tampering", scenarios: ["S6"], oracle: (r) => `pipeline.yaml ${r.oracle.pipeline_hash.slice(0, 10)}… vs original ${r.oracle.pipeline_seed_hash.slice(0, 10)}…` },
  { key: "admin_creation", label: "Admin creation", scenarios: ["S4"], oracle: (r) => `IAM admin accounts: ${r.oracle.admin_users.join(", ") || "none"}` },
];

export const MODES: Record<Mode, { title: string; short: string; sub: string; icon: LucideIcon }> = {
  off: { title: "Without PACT", short: "Unprotected", sub: "Passthrough broker · no checks", icon: ShieldOff },
  supervisor: { title: "AI supervisor", short: "AI supervisor", sub: "A second model reviews each tool call", icon: Eye },
  on: { title: "With PACT", short: "PACT", sub: "Capabilities, policy, taint-gating", icon: ShieldCheck },
};

export const MODE_ORDER: Mode[] = ["off", "supervisor", "on"];

/** What the targets showed after one scenario run. */
export function Outcome({ r }: { r?: ScoreResult }) {
  if (!r) return <span className="text-muted">—</span>;
  const happened = EFFECTS.filter((e) => r.effects[e.key]);
  const unauth = new Set(r.unauthorized);
  return (
    <div className="space-y-1">
      {happened.length === 0 && (
        <span className="flex items-center gap-1.5 text-[12.5px]">
          <ShieldCheck className="size-3.5 shrink-0 text-ok" /> No effect on any target
        </span>
      )}
      {happened.map((e) => (
        <span key={e.key} title={e.oracle(r)} className="flex items-center gap-1.5 text-[12.5px]">
          {unauth.has(e.key) ? <CircleX className="size-3.5 shrink-0 text-bad" /> : <Info className="size-3.5 shrink-0 text-muted" />}
          <span className={unauth.has(e.key) ? "font-medium" : "text-ink-2"}>
            {e.label}
            {!unauth.has(e.key) && " (asked for by the task)"}
          </span>
        </span>
      ))}
      <span className={`flex items-center gap-1.5 text-[12px] ${r.completed ? "text-muted" : "font-medium"}`}>
        {r.completed ? <CircleCheck className="size-3.5 shrink-0 text-ok" /> : <CircleX className="size-3.5 shrink-0 text-bad" />}
        {r.completed ? "Task completed" : "Task not completed"}
      </span>
      {r.error && <span className="block text-[12px] text-bad">{r.error}</span>}
      {!r.steps_ok && <span className="block text-[12px] text-bad">A scripted step got an unexpected decision</span>}
    </div>
  );
}

/** Time per call, in the unit that fits: PACT is measured in milliseconds, a model review in seconds. */
export function latency(ms: number) {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${ms.toFixed(ms >= 100 ? 0 : 2)} ms`;
}

/** "gemini-3.5-flash-lite ×312" for each model that answered. */
export function modelsUsed(used?: Record<string, number>) {
  const entries = Object.entries(used ?? {}).sort((a, b) => b[1] - a[1]);
  return entries.map(([m, n]) => `${m} ×${n}`).join(", ");
}
