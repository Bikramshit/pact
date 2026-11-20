import { Ban, CircleCheck, EyeOff, Flag, ShieldAlert, TriangleAlert, UserCheck, type LucideIcon } from "lucide-react";
import type { Contract } from "@/lib/types";

function Section({ icon: Icon, tone, title, hint, items, variant }: {
  icon: LucideIcon; tone: string; title: string; hint: string; items: string[]; variant: "allow" | "deny" | "warn" | "plain";
}) {
  const tok = {
    allow: "border-ok/25 bg-ok-soft",
    deny: "border-bad/25 bg-bad-soft",
    warn: "border-warn/30 bg-warn-soft",
    plain: "border-line bg-raised",
  }[variant];
  return (
    <div className="grid grid-cols-[20px_1fr] gap-x-3 py-3">
      <Icon className={`mt-0.5 size-4 ${tone}`} />
      <div className="min-w-0">
        <p className="text-[13px] font-semibold">
          {title} <span className="font-normal text-muted">· {hint}</span>
        </p>
        <div className="mt-1.5 flex flex-wrap gap-1.5">
          {items.length === 0 ? <span className="text-[12.5px] text-muted">None</span> : items.map((i) => (
            <span key={i} className={`tok ${tok}`}>
              {i}
            </span>
          ))}
        </div>
      </div>
    </div>
  );
}

/** The contract as the user reviews it: scope, denials and the trust labels that drive taint-gating. */
export function ContractView({ contract }: { contract: Contract }) {
  const action = contract.on_taint === "deny" ? "denied" : "held for you";
  return (
    <div className="divide-y divide-line">
      <Section icon={CircleCheck} tone="text-ok" variant="allow" title="Allowed" hint="what the agent may do" items={contract.allow.flatMap((g) => g.ops.map((op) => `${g.resource}:${op}`))} />
      <Section icon={Ban} tone="text-bad" variant="deny" title="Denied" hint="never, whatever it asks" items={contract.deny} />
      <Section icon={TriangleAlert} tone="text-warn" variant="warn" title="Untrusted content" hint="reading it taints the session" items={contract.untrusted} />
      <Section icon={ShieldAlert} tone="text-warn" variant="warn" title="Sensitive actions" hint={`${action} once tainted`} items={contract.sensitive} />
      {(contract.confidential?.length ?? 0) > 0 && (
        <Section icon={EyeOff} tone="text-warn" variant="warn" title="Confidential data" hint={`after reading it, anything sent out is ${action}`} items={contract.confidential!} />
      )}
      <Section icon={Flag} tone="text-ink-2" variant="plain" title="Taint-tolerant" hint="the intended outcome; still allowed" items={contract.taint_tolerant} />
      {contract.approval_required.length > 0 && (
        <Section icon={UserCheck} tone="text-warn" variant="warn" title="Always needs approval" hint="held even when clean" items={contract.approval_required} />
      )}
    </div>
  );
}
