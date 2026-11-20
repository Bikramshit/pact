"""Agent entry point: register a public key, then work through assignments from the broker."""

import os
import sys
import time

from .broker import Broker
from .replay import run_replay


def log(message: str) -> None:
    print(message, flush=True)


def main() -> int:
    base_url = os.environ.get("PACT_BROKER", "http://pact-core:9090")
    model = os.environ.get("GEMINI_MODEL", "gemini-3.8-flash")
    broker = Broker(base_url)
    agent_id = broker.register_until_ready(log)
    log(f"registered as {agent_id} with {broker.signer.public_key}")

    while True:
        try:
            assignment = broker.next_assignment()
        except Exception as exc:
            log(f"polling failed ({exc.__class__.__name__}: {exc}); retrying")
            time.sleep(2)
            continue
        if not assignment:
            continue
        kind = assignment.get("kind", "live")
        log(f"assignment {assignment['assignment_id']} ({kind}) {assignment.get('label', '')}")
        try:
            if kind == "replay":
                report = run_replay(broker, assignment, log)
            else:
                from .loop import run_live  # imported lazily so replay works without the SDK loaded
                report = run_live(broker, assignment, model, log)
        except Exception as exc:
            report = {"status": "failed", "summary": "", "steps": [], "llm_calls": 0, "error": f"{exc.__class__.__name__}: {exc}"[:500]}
        log(f"finished: {report['status']} {report.get('error', '')}")
        try:
            broker.done(assignment["assignment_id"], report)
        except Exception as exc:
            log(f"could not report completion: {exc}")


if __name__ == "__main__":
    sys.exit(main())
