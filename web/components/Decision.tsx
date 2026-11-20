import type { EvidenceRecord } from "@/lib/types";
import type { Tone } from "./ui";

/** What a record means to a viewer: allowed, denied, held, or a hold's resolution. */
export function verdict(r: EvidenceRecord): { text: string; tone: Tone } {
  if (r.decision === "allow") return { text: "Allowed", tone: "ok" };
  if (r.decision === "deny") return { text: "Denied", tone: "bad" };
  if (r.decision === "hold") {
    if (!r.approval) return { text: "Held", tone: "warn" };
    if (r.approval.result === "approved") return { text: "Approved", tone: "ok" };
    return { text: r.approval.result === "timeout" ? "Timed out" : "Rejected", tone: "bad" };
  }
  return { text: r.decision, tone: "neutral" };
}

/** Plain-language reading of a broker reason code. */
export function explain(reason: string): string {
  const [code, rest] = reason.split(/:\s(.+)/);
  switch (code) {
    case "contract_deny":
      return `The contract denies ${rest}`;
    case "no_capability_scope":
      return `The capability has no scope for ${rest}`;
    case "not_in_contract":
      return `The contract does not grant ${rest}`;
    case "tainted_session":
      return "Untrusted content was read earlier in this session, and this is a sensitive action";
    case "confidential_session":
      return "Confidential data was read earlier in this session, and this sends data out";
    case "taint_tolerant":
      return "Allowed after untrusted content was read: this action is the task's intended outcome";
    case "approval_required":
      return "The contract requires a human to approve this action";
    case "hold_rejected":
      return "A human rejected this action";
    case "hold_timeout":
      return "Nobody approved this action in time";
    case "bad_capability_signature":
      return "The capability's signature is not valid: it was altered";
    case "bad_request_signature":
      return "The request was not signed by the key the capability is bound to";
    case "nonce_reused":
      return "This exact request was already sent (replay)";
    case "stale_timestamp":
      return "The request timestamp is more than 60 seconds off";
    case "capability_expired":
      return "The capability has expired";
    case "capability_revoked":
      return "The capability was revoked";
    case "unknown_capability":
      return "The capability was not issued by this broker";
    case "missing_header":
      return `The request is missing ${rest}`;
    case "invalid_request":
      return `The request is malformed: ${rest}`;
    default:
      return reason;
  }
}
