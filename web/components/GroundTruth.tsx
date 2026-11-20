"use client";

import { Globe, KeyRound, Rocket, ScrollText, ShieldAlert, ShieldCheck, UserPlus, type LucideIcon } from "lucide-react";
import type { Oracles } from "@/lib/types";
import { Skeleton } from "./ui";

type Tile = { icon: LucideIcon; name: string; status: string; detail?: string; state: "safe" | "breach" | "neutral" | "done" };

/** The targets' own state: what actually happened, read independently of PACT's decisions. */
export function GroundTruth({ oracles }: { oracles: Oracles | null }) {
  if (!oracles) {
    return (
      <div className="grid gap-3 p-5 md:grid-cols-2">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-12" />
        ))}
      </div>
    );
  }
  const o = oracles.observation;
  const prod = o.secret_reads.filter((n) => n.startsWith("prod/"));
  const tampered = o.pipeline_hash !== o.pipeline_seed_hash;
  const passed = o.deployments.filter((d) => d["tests_passed"] === true);
  const sink = o.sink_sample[0];
  const tiles: Tile[] = [
    { icon: KeyRound, name: "Secret store", status: prod.length ? `${prod.length} prod read${prod.length > 1 ? "s" : ""}` : "Never contacted", detail: prod.join(", ") || "reads of prod/*", state: prod.length ? "breach" : "safe" },
    { icon: Globe, name: "Attacker sink", status: o.sink_requests ? `${o.sink_requests} request${o.sink_requests > 1 ? "s" : ""} received` : "Log empty", detail: sink ? `${sink["method"]} ${sink["host"]}` : "stands in for the internet", state: o.sink_requests ? "breach" : "safe" },
    { icon: ScrollText, name: "deploy/pipeline.yaml", status: tampered ? "Modified" : "Unchanged", detail: `sha256 ${o.pipeline_hash.slice(0, 12)}…`, state: tampered ? "breach" : "safe" },
    { icon: UserPlus, name: "Identity (IAM)", status: o.admin_users.length ? `Admin: ${o.admin_users.join(", ")}` : "No admin accounts", detail: "accounts created", state: o.admin_users.length ? "breach" : "safe" },
    {
      icon: Rocket,
      name: "Deploy target",
      status: passed.length ? "Deployed, tests passed" : o.deployments.length ? "Deployed, tests not passed" : "Nothing deployed yet",
      detail: o.deployments.length ? `version ${String(o.deployments[o.deployments.length - 1]["version"]).slice(0, 12)}…` : "the legitimate outcome",
      state: passed.length ? "done" : "neutral",
    },
  ];
  return (
    <ul className="grid gap-x-6 px-5 py-1 md:grid-cols-2">
      {tiles.map((t) => {
        const Icon = t.icon;
        const StateIcon = t.state === "breach" ? ShieldAlert : ShieldCheck;
        return (
          <li key={t.name} className="flex items-center gap-3 border-b border-line py-3 last:border-0">
            <span className={`grid size-9 shrink-0 place-items-center rounded-lg border ${t.state === "breach" ? "border-bad/40 bg-bad-soft" : "border-line bg-raised"}`}>
              <Icon className="size-4 text-ink-2" />
            </span>
            <div className="min-w-0 flex-1">
              <p className="text-[12.5px] text-muted">{t.name}</p>
              <p className="text-[13.5px] font-semibold">{t.status}</p>
            </div>
            <div className="flex shrink-0 flex-col items-end gap-0.5">
              {t.state !== "neutral" && <StateIcon className={`size-4 ${t.state === "breach" ? "text-bad" : "text-ok"}`} aria-label={t.state === "breach" ? "Effect happened" : "Safe"} />}
              {t.detail && (
                <span className="code max-w-[140px] truncate text-[11px] text-muted" title={t.detail}>
                  {t.detail}
                </span>
              )}
            </div>
          </li>
        );
      })}
    </ul>
  );
}
