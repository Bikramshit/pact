"use client";

import { Bot, FileCode2, FlaskConical, Globe, KeyRound, Rocket, ShieldCheck, Sparkles, UserPlus, type LucideIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

/*
 * The decision-flow illustration: the agent on the left, the PACT broker in the
 * middle, the protected resources on the right. Every tool call travels from
 * the agent into the broker; only allowed calls continue to their target.
 * Used live on the Run page (driven by real decisions) and as an animated
 * explainer on the Overview page.
 */

export type TargetId = "repo" | "tests" | "deploy" | "model" | "secrets" | "network" | "iam";
export type Outcome = "allow" | "deny" | "hold" | "approved" | "rejected" | "unchecked";
export type Pulse = { id: string; target: TargetId; outcome: Outcome; label: string };
export type TargetCounts = Partial<Record<TargetId, { ok: number; blocked: number }>>;

type TargetDef = { id: TargetId; label: string; sub: string; icon: LucideIcon; type: string };

export const TARGETS: TargetDef[] = [
  { id: "repo", label: "Repository", sub: "source files", icon: FileCode2, type: "repo" },
  { id: "tests", label: "Test runner", sub: "unit tests", icon: FlaskConical, type: "tests" },
  { id: "deploy", label: "Deploy target", sub: "production release", icon: Rocket, type: "deploy" },
  { id: "model", label: "Gemini", sub: "model calls", icon: Sparkles, type: "llm" },
  { id: "secrets", label: "Secret store", sub: "prod credentials", icon: KeyRound, type: "secret" },
  { id: "network", label: "Internet", sub: "external hosts", icon: Globe, type: "net" },
  { id: "iam", label: "Identity (IAM)", sub: "admin accounts", icon: UserPlus, type: "iam" },
];

export function targetOf(tool: string): TargetId {
  switch (tool) {
    case "run_tests":
      return "tests";
    case "deploy":
      return "deploy";
    case "llm_generate":
      return "model";
    case "read_secret":
      return "secrets";
    case "http_request":
      return "network";
    case "create_user":
      return "iam";
    default:
      return "repo";
  }
}

const DEFAULT_GRANTED = ["repo", "tests", "deploy", "llm"];

// Geometry (viewBox units).
const W = 780;
const H = 410;
const MID = 214;
const AGENT = { x: 22, y: MID - 56, w: 154, h: 112 };
const BROKER = { x: 296, y: MID - 142, w: 186, h: 284 };
const TX = 594;
const TW = 180;
const TH = 40;

function bez(p0: number, p1: number, p2: number, p3: number, t: number) {
  const u = 1 - t;
  return u * u * u * p0 + 3 * u * u * t * p1 + 3 * u * t * t * p2 + t * t * t * p3;
}

function edgePath(cy: number) {
  const x0 = BROKER.x + BROKER.w;
  return `M${x0},${MID} C${x0 + 64},${MID} ${TX - 64},${cy} ${TX},${cy}`;
}

function edgePoint(cy: number, t: number) {
  const x0 = BROKER.x + BROKER.w;
  return { x: bez(x0, x0 + 64, TX - 64, TX, t), y: bez(MID, MID, cy, cy, t) };
}

const STROKE: Record<Outcome, string> = {
  allow: "stroke-ok",
  approved: "stroke-ok",
  deny: "stroke-bad",
  rejected: "stroke-bad",
  hold: "stroke-warn",
  unchecked: "stroke-muted",
};
const FILL: Record<Outcome, string> = {
  allow: "fill-ok",
  approved: "fill-ok",
  deny: "fill-bad",
  rejected: "fill-bad",
  hold: "fill-warn",
  unchecked: "fill-muted",
};
const LABEL: Record<Outcome, string> = {
  allow: "ALLOWED",
  approved: "APPROVED",
  deny: "DENIED",
  rejected: "REJECTED",
  hold: "HELD",
  unchecked: "UNCHECKED",
};

/** A dot that travels along a path once. */
function Packet({ d, dur, outcome }: { d: string; dur: number; outcome: Outcome }) {
  const motion = useRef<SVGAnimateMotionElement>(null);
  const show = useRef<SVGAnimateElement>(null);
  useEffect(() => {
    show.current?.beginElement();
    motion.current?.beginElement();
  }, []);
  return (
    <g opacity={0}>
      <animate ref={show} attributeName="opacity" from="1" to="1" dur={`${dur}s`} begin="indefinite" fill="remove" />
      <circle r={9} className={`${FILL[outcome]} opacity-20`} />
      <circle r={4.5} className={`${FILL[outcome]} stroke-surface`} strokeWidth={2} />
      <animateMotion ref={motion} path={d} dur={`${dur}s`} begin="indefinite" fill="freeze" calcMode="spline" keyTimes="0;1" keySplines="0.45 0 0.25 1" />
    </g>
  );
}

function Mark({ x, y, outcome }: { x: number; y: number; outcome: Outcome }) {
  const bad = outcome === "deny" || outcome === "rejected";
  return (
    <g className="pop" style={{ animationDelay: "0.45s", opacity: 0 }}>
      <circle cx={x} cy={y} r={11} className={`${FILL[outcome]} stroke-surface`} strokeWidth={3} />
      {bad ? (
        <path d={`M${x - 4},${y - 4} L${x + 4},${y + 4} M${x + 4},${y - 4} L${x - 4},${y + 4}`} className="stroke-white" strokeWidth={2.2} strokeLinecap="round" />
      ) : (
        <path d={`M${x - 3},${y - 4} L${x - 3},${y + 4} M${x + 3},${y - 4} L${x + 3},${y + 4}`} className="stroke-white" strokeWidth={2.2} strokeLinecap="round" />
      )}
    </g>
  );
}

export function FlowMap({
  pulses,
  counts = {},
  granted = DEFAULT_GRANTED,
  last,
  pendingHolds = [],
  tainted,
  title = "Decision flow",
}: {
  pulses: Pulse[];
  counts?: TargetCounts;
  granted?: string[];
  last?: { outcome: Outcome; label: string } | null;
  pendingHolds?: TargetId[];
  tainted?: string | null;
  title?: string;
}) {
  const [reduced, setReduced] = useState(false);
  useEffect(() => {
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    setReduced(mq.matches);
  }, []);

  // Granted targets first, then denied ones, each group with a caption.
  const layout = useMemo(() => {
    const g = TARGETS.filter((t) => granted.includes(t.type));
    const d = TARGETS.filter((t) => !granted.includes(t.type));
    const step = 46;
    const top = 44;
    const rows: { def: TargetDef; y: number; granted: boolean }[] = [];
    g.forEach((def, i) => rows.push({ def, y: top + i * step, granted: true }));
    const dTop = top + g.length * step + 30;
    d.forEach((def, i) => rows.push({ def, y: dTop + i * step, granted: false }));
    return { rows, deniedCaption: dTop - 12, hasDenied: d.length > 0 };
  }, [granted]);

  const row = (id: TargetId) => layout.rows.find((r) => r.def.id === id)!;
  const cy = (id: TargetId) => row(id).y + TH / 2;

  // Each recent pulse lights its edge, its target and the broker checks.
  const latest = pulses[pulses.length - 1];

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="block h-auto w-full select-none" role="img" aria-label={`${title}: AI agent, PACT broker and protected resources`}>
      <defs>
        <pattern id="dots" width="16" height="16" patternUnits="userSpaceOnUse">
          <circle cx="1.5" cy="1.5" r="1" className="fill-line" />
        </pattern>
        <linearGradient id="brokerFill" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="var(--color-accent-soft)" />
          <stop offset="1" stopColor="var(--color-surface)" />
        </linearGradient>
      </defs>
      <rect x="0" y="0" width={W} height={H} fill="url(#dots)" opacity="0.7" />

      {/* Agent sandbox */}
      <rect x={AGENT.x - 14} y={AGENT.y - 40} width={AGENT.w + 28} height={AGENT.h + 80} rx="18" className="fill-surface/60 stroke-line-strong" strokeDasharray="5 5" />
      <text x={AGENT.x - 2} y={AGENT.y - 22} className="fill-muted font-mono text-[10.5px] tracking-[0.08em]">
        UNTRUSTED
      </text>
      <rect x={AGENT.x} y={AGENT.y} width={AGENT.w} height={AGENT.h} rx="14" className="fill-surface stroke-line-strong" />
      <rect x={AGENT.x + 14} y={AGENT.y + 16} width="38" height="38" rx="10" className="fill-inverse" />
      <Bot x={AGENT.x + 22} y={AGENT.y + 24} width={22} height={22} className="text-on-inverse" strokeWidth={1.8} />
      <text x={AGENT.x + 62} y={AGENT.y + 32} className="fill-ink text-[14.5px] font-semibold">
        AI agent
      </text>
      <text x={AGENT.x + 62} y={AGENT.y + 48} className="fill-muted text-[11.5px]">
        Gemini tool loop
      </text>
      <text x={AGENT.x + 14} y={AGENT.y + 78} className="fill-ink-2 text-[11.5px]">
        no keys · no repo copy
      </text>
      <text x={AGENT.x + 14} y={AGENT.y + 94} className="fill-ink-2 text-[11.5px]">
        no route to targets
      </text>
      {tainted && (
        <g className="animate-fade">
          <rect x={AGENT.x - 6} y={AGENT.y + AGENT.h + 10} width={AGENT.w + 12} height="24" rx="12" className="fill-warn-soft stroke-warn" strokeWidth="1" />
          <circle cx={AGENT.x + 9} cy={AGENT.y + AGENT.h + 22} r="3.5" className="fill-warn" />
          <text x={AGENT.x + 19} y={AGENT.y + AGENT.h + 26} className="fill-ink text-[11.5px] font-semibold">
            Session tainted
          </text>
        </g>
      )}

      {/* Agent → broker */}
      <path d={`M${AGENT.x + AGENT.w},${MID} L${BROKER.x},${MID}`} className="stroke-line-strong" strokeWidth="1.5" fill="none" />
      <text x={(AGENT.x + AGENT.w + BROKER.x) / 2} y={MID - 10} textAnchor="middle" className="fill-muted text-[11px]">
        signed request
      </text>

      {/* Broker → targets */}
      {layout.rows.map(({ def, y, granted }) => (
        <path key={def.id} d={edgePath(y + TH / 2)} fill="none" strokeWidth="1.5" className={granted ? "stroke-line-strong" : "stroke-line"} strokeDasharray={granted ? undefined : "3 5"} />
      ))}

      {/* Edge highlights for recent decisions */}
      {pulses.map((p) => {
        const passes = p.outcome === "allow" || p.outcome === "approved" || p.outcome === "unchecked";
        const pt = edgePoint(cy(p.target), 0.32);
        return (
          <g key={`e-${p.id}`}>
            <path d={`M${AGENT.x + AGENT.w},${MID} L${BROKER.x},${MID}`} className={`${STROKE[p.outcome]} flash`} strokeWidth="2.5" fill="none" />
            {passes ? (
              <path d={edgePath(cy(p.target))} className={`${STROKE[p.outcome]} flash`} style={{ animationDelay: "0.35s", opacity: 0 }} strokeWidth="2.5" fill="none" />
            ) : (
              p.outcome !== "hold" && <Mark x={pt.x} y={pt.y} outcome={p.outcome} />
            )}
          </g>
        );
      })}

      {/* Pending holds keep their edge marked until a human decides */}
      {pendingHolds.map((id) => {
        const pt = edgePoint(cy(id), 0.32);
        return (
          <g key={`h-${id}`}>
            <path d={edgePath(cy(id))} className="stroke-warn flow-dash" strokeWidth="2" fill="none" />
            <circle cx={pt.x} cy={pt.y} r="11" className="fill-warn stroke-surface" strokeWidth="3" />
            <path d={`M${pt.x - 3},${pt.y - 4} L${pt.x - 3},${pt.y + 4} M${pt.x + 3},${pt.y - 4} L${pt.x + 3},${pt.y + 4}`} className="stroke-white" strokeWidth="2.2" strokeLinecap="round" />
          </g>
        );
      })}

      {/* Packets travel under the broker box */}
      {!reduced &&
        pulses.map((p) => {
          const passes = p.outcome === "allow" || p.outcome === "approved" || p.outcome === "unchecked";
          const start = p.outcome === "approved" ? `M${BROKER.x + BROKER.w - 10},${MID}` : `M${AGENT.x + AGENT.w},${MID}`;
          const d = passes
            ? `${start} L${BROKER.x + BROKER.w},${MID} ${edgePath(cy(p.target)).replace(/^M[^ ]+ /, "")}`
            : `M${AGENT.x + AGENT.w},${MID} L${BROKER.x + 18},${MID}`;
          return <Packet key={`p-${p.id}`} d={d} dur={passes ? 1.25 : 0.55} outcome={p.outcome} />;
        })}

      {/* Broker */}
      <rect x={BROKER.x} y={BROKER.y} width={BROKER.w} height={BROKER.h} rx="18" fill="url(#brokerFill)" className="stroke-accent" strokeWidth="1.5" />
      <rect x={BROKER.x + 16} y={BROKER.y + 16} width="38" height="38" rx="11" className="fill-accent" />
      <ShieldCheck x={BROKER.x + 24} y={BROKER.y + 24} width={22} height={22} className="text-white" strokeWidth={1.9} />
      <text x={BROKER.x + 64} y={BROKER.y + 33} className="fill-ink text-[15.5px] font-semibold">
        PACT broker
      </text>
      <text x={BROKER.x + 64} y={BROKER.y + 49} className="fill-muted font-mono text-[10.5px]">
        the only door
      </text>
      {["Capability signature", "Request signature", "Contract policy", "Evidence record"].map((label, i) => {
        const y = BROKER.y + 74 + i * 33;
        return (
          <g key={label}>
            <rect x={BROKER.x + 14} y={y} width={BROKER.w - 28} height="27" rx="8" className="fill-surface stroke-line" />
            <circle cx={BROKER.x + 29} cy={y + 13.5} r="5" className="fill-line" />
            {latest && (
              <circle key={`${latest.id}-${i}`} cx={BROKER.x + 29} cy={y + 13.5} r="5" className={`${i === 2 ? FILL[latest.outcome] : "fill-ok"} flash`} style={{ animationDelay: `${0.25 + i * 0.08}s`, animationDuration: "2.2s", opacity: 0 }} />
            )}
            <text x={BROKER.x + 42} y={y + 17.5} className="fill-ink-2 text-[11.5px]">
              {label}
            </text>
          </g>
        );
      })}
      <g>
        <rect x={BROKER.x + 14} y={BROKER.y + BROKER.h - 56} width={BROKER.w - 28} height="40" rx="10" className={last ? "fill-inverse" : "fill-subtle"} />
        {last ? (
          <>
            <circle cx={BROKER.x + 31} cy={BROKER.y + BROKER.h - 36} r="5" className={FILL[last.outcome]} />
            <text x={BROKER.x + 44} y={BROKER.y + BROKER.h - 39} className="fill-on-inverse font-mono text-[10.5px] font-semibold tracking-[0.06em]">
              {LABEL[last.outcome]}
            </text>
            <text x={BROKER.x + 44} y={BROKER.y + BROKER.h - 25} className="fill-on-inverse text-[11px] opacity-75">
              {last.label.length > 20 ? last.label.slice(0, 19) + "…" : last.label}
            </text>
          </>
        ) : (
          <text x={BROKER.x + BROKER.w / 2} y={BROKER.y + BROKER.h - 32} textAnchor="middle" className="fill-muted text-[11.5px]">
            Waiting for a request
          </text>
        )}
      </g>

      {/* Targets */}
      <text x={TX} y={layout.rows[0].y - 12} className="fill-muted font-mono text-[10.5px] tracking-[0.08em]">
        GRANTED BY THE CONTRACT
      </text>
      {layout.hasDenied && (
        <text x={TX} y={layout.deniedCaption} className="fill-muted font-mono text-[10.5px] tracking-[0.08em]">
          DENIED BY THE CONTRACT
        </text>
      )}
      {layout.rows.map(({ def, y, granted }) => {
        const Icon = def.icon;
        const c = counts[def.id];
        const hit = pulses.filter((p) => p.target === def.id).slice(-1)[0];
        const reached = hit && (hit.outcome === "allow" || hit.outcome === "approved" || hit.outcome === "unchecked");
        return (
          <g key={def.id}>
            <rect x={TX} y={y} width={TW} height={TH} rx="10" className={granted ? "fill-surface stroke-line-strong" : "fill-subtle stroke-line"} strokeDasharray={granted ? undefined : "4 4"} />
            {reached && (
              <rect key={hit.id} x={TX} y={y} width={TW} height={TH} rx="10" className={`${hit.outcome === "unchecked" ? "fill-muted/20 stroke-muted" : "fill-ok/15 stroke-ok"} flash`} style={{ animationDelay: "1.05s", animationDuration: "1.8s", opacity: 0 }} strokeWidth="1.5" />
            )}
            <Icon x={TX + 12} y={y + 11.5} width={17} height={17} className={granted ? "text-ink-2" : "text-muted"} strokeWidth={1.8} />
            <text x={TX + 38} y={y + 18} className={`text-[12.5px] font-semibold ${granted ? "fill-ink" : "fill-ink-2"}`}>
              {def.label}
            </text>
            <text x={TX + 38} y={y + 31} className="fill-muted text-[10.5px]">
              {def.sub}
            </text>
            {c && c.blocked > 0 && (
              <g>
                <rect x={TX + TW - 38} y={y + 10} width="30" height="20" rx="10" className="fill-bad-soft" />
                <text x={TX + TW - 23} y={y + 24} textAnchor="middle" className="fill-ink font-mono text-[11px] font-semibold">
                  ✕{c.blocked}
                </text>
              </g>
            )}
            {c && c.ok > 0 && (
              <g>
                <rect x={TX + TW - (c.blocked > 0 ? 72 : 38)} y={y + 10} width="30" height="20" rx="10" className="fill-ok-soft" />
                <text x={TX + TW - (c.blocked > 0 ? 57 : 23)} y={y + 24} textAnchor="middle" className="fill-ink font-mono text-[11px] font-semibold">
                  ✓{c.ok > 99 ? "99" : c.ok}
                </text>
              </g>
            )}
          </g>
        );
      })}
    </svg>
  );
}

/** Legend for the flow map. */
export function FlowLegend() {
  const items = [
    { cls: "bg-ok", label: "Allowed: reaches the target" },
    { cls: "bg-bad", label: "Denied: never leaves the broker" },
    { cls: "bg-warn", label: "Held: waits for a human" },
  ];
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-ink-2">
      {items.map((i) => (
        <span key={i.label} className="inline-flex items-center gap-1.5">
          <span className={`size-2 rounded-full ${i.cls}`} />
          {i.label}
        </span>
      ))}
    </div>
  );
}

const DEMO: { target: TargetId; outcome: Outcome; label: string }[] = [
  { target: "repo", outcome: "allow", label: "read_file README.md" },
  { target: "tests", outcome: "allow", label: "run_tests" },
  { target: "repo", outcome: "allow", label: "read_file deps/…/README.md" },
  { target: "secrets", outcome: "deny", label: "read_secret prod/db-password" },
  { target: "network", outcome: "deny", label: "http_request POST" },
  { target: "repo", outcome: "hold", label: "write_file deploy/pipeline.yaml" },
  { target: "repo", outcome: "rejected", label: "write_file deploy/pipeline.yaml" },
  { target: "repo", outcome: "allow", label: "write_file app/cart.py" },
  { target: "deploy", outcome: "allow", label: "deploy web-app" },
  { target: "iam", outcome: "deny", label: "create_user admin" },
];

/** The flow map playing a scripted sequence on a loop, as an explainer. */
export function FlowMapDemo() {
  const [step, setStep] = useState(0);
  const [pulses, setPulses] = useState<Pulse[]>([]);
  const [counts, setCounts] = useState<TargetCounts>({});
  const [hold, setHold] = useState<TargetId | null>(null);
  const [tainted, setTainted] = useState(false);
  const [last, setLast] = useState<{ outcome: Outcome; label: string } | null>(null);

  useEffect(() => {
    const t = setTimeout(() => {
      const i = step % (DEMO.length + 2);
      if (i >= DEMO.length) {
        if (i === DEMO.length + 1) {
          setCounts({});
          setTainted(false);
          setLast(null);
          setPulses([]);
        }
        setStep(step + 1);
        return;
      }
      const ev = DEMO[i];
      const id = `${step}`;
      setPulses((all) => [...all.slice(-3), { id, ...ev }]);
      setLast({ outcome: ev.outcome, label: ev.label });
      setHold(ev.outcome === "hold" ? ev.target : null);
      if (ev.label.includes("deps/")) setTainted(true);
      if (ev.outcome !== "hold") {
        setCounts((c) => {
          const cur = c[ev.target] ?? { ok: 0, blocked: 0 };
          const ok = ev.outcome === "allow" || ev.outcome === "approved";
          return { ...c, [ev.target]: { ok: cur.ok + (ok ? 1 : 0), blocked: cur.blocked + (ok ? 0 : 1) } };
        });
      }
      setStep(step + 1);
    }, step === 0 ? 600 : 1700);
    return () => clearTimeout(t);
  }, [step]);

  return <FlowMap pulses={pulses} counts={counts} last={last} pendingHolds={hold ? [hold] : []} tainted={tainted ? "deps/payments-sdk/README.md" : null} title="Illustration of PACT's decision flow" />;
}
