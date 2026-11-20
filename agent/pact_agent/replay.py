"""Scripted agent: plays a fixed list of tool calls and reports what the broker answered.

Replay is the deterministic half of every acceptance test. PACT's claim is that
enforcement holds whatever the model does, so a scripted malicious request is a
fair stand-in for a model that followed an injected instruction.
"""

from .broker import Broker


def run_replay(broker: Broker, assignment: dict, log=print) -> dict:
    capability = assignment["capability"]
    steps = []
    status = "completed"
    error = ""
    for step in assignment.get("steps") or []:
        tool, args = step["tool"], step.get("args") or {}
        try:
            d = broker.call_tool(capability, tool, args, step.get("tamper", ""))
            entry = {"tool": tool, "status": d.status, "decision": d.decision, "reasons": d.reasons, "held": d.held}
            if d.error:
                entry["error"] = str(d.error)[:300]
        except Exception as exc:
            entry = {"tool": tool, "status": 0, "decision": "error", "error": str(exc)[:300]}
            status, error = "failed", str(exc)[:300]
        log(f"  {tool:13s} -> {entry['decision']} {' '.join(entry.get('reasons') or [])}")
        steps.append(entry)
    return {"status": status, "summary": f"replayed {len(steps)} scripted tool calls", "steps": steps, "llm_calls": 0, "error": error}
