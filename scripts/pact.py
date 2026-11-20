#!/usr/bin/env python3
"""Command-line driver for a running PACT stack (standard library only).

    python3 scripts/pact.py replay [S1 S2 ...]
    python3 scripts/pact.py scoreboard [--agent replay|live] [--quick]
    python3 scripts/pact.py verify
"""

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request

API = os.environ.get("API", "http://127.0.0.1:8080")
NAMES = {"off": "without PACT", "supervisor": "AI supervisor", "on": "with PACT"}


def call(path, body=None, timeout=900):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(API + path, data=data, headers={"Content-Type": "application/json"} if data else {})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as exc:
        try:
            detail = json.load(exc).get("error", "")
        except Exception:
            detail = ""
        sys.exit(f"{path}: HTTP {exc.code} {detail}")
    except urllib.error.URLError as exc:
        sys.exit(f"control plane not reachable at {API} ({exc.reason}); is the stack up?")


def cmd_replay(args):
    out = call("/v1/replay", {"scenarios": args.scenarios})
    for res in out["results"]:
        ok = res["steps_ok"] and not res["unauthorized"] and res["agent_status"] == "done" and not res.get("error")
        print(f"\n{res['scenario']:5s} {res['title']}   {'PASS' if ok else 'FAIL'}")
        for st in res.get("steps") or []:
            mark = "ok  " if st["ok"] else "FAIL"
            reasons = "; ".join(st.get("reasons") or [])
            print(f"   {mark} {st['tool']:13s} expected {st.get('expect', '-'):14s} got {st['decision']:14s} {reasons}")
            if not st["ok"]:
                print(f"        {st.get('why', '')}")
        authorised = [e for e, happened in res["effects"].items() if happened and e not in res["unauthorized"]]
        note = f"; authorised by the task: {authorised}" if authorised else ""
        print(f"   targets: unauthorised effects {res['unauthorized'] or 'none'}{note}; task completed: {res['completed']}")
        if res.get("error"):
            print(f"   error: {res['error']}")
    print("\nREPLAY PASSED: every step got its expected decision." if out["ok"] else "\nREPLAY FAILED")
    return 0 if out["ok"] else 1


def cmd_scoreboard(args):
    run = call("/v1/scoreboard/runs", {"agent": args.agent, "quick": args.quick, "supervisor": args.supervisor})
    print(f"run {run['run_id']}: {run['total']} scenario runs ({run['scenarios']} scenarios x {len(run['modes'])} configurations), agent={args.agent}")
    seen = 0
    while True:
        time.sleep(1)
        run = call("/v1/scoreboard/latest")["run"]
        results = run.get("results") or []
        for res in results[seen:]:
            eff = ", ".join(res["unauthorized"]) or "none"
            err = f"  ERROR {res['error']}" if res.get("error") else ""
            print(f"  {NAMES[res['mode']]:14s} {res['scenario']}-{res['variant']}  unauthorised effects: {eff:32s} completed: {res['completed']}{err}")
        seen = len(results)
        if run["status"] != "running":
            break
    print(f"\nMeasured on our {run['scenarios']} seed scenarios at {run.get('finished_at', '')} (agent: {run['agent_kind']} {run.get('model', '')})")
    if run.get("models_used"):
        print("Model calls answered by: " + ", ".join(f"{m} x{n}" for m, n in run["models_used"].items()))
    rows = [
        ("attack attempts with an unauthorised effect", lambda s: f"{s['attacks_with_effect']} / {s['attack_attempts']}"),
        ("legitimate tasks completed", lambda s: f"{s['tasks_completed']} / {s['tasks']}"),
        ("benign actions held or denied", lambda s: str(s["benign_blocked"])),
        ("holds auto-rejected", lambda s: str(s["holds_auto_rejected"])),
        ("median added time per call (ms)", lambda s: f"{s['median_overhead_ms']:.2f}"),
        ("scenario runs with errors", lambda s: str(s["errors"])),
    ]
    modes = [m for m in ("off", "supervisor", "on") if m in run["summary"]]
    print(f"{'':46s}" + "".join(f"{NAMES[m]:>16s}" for m in modes))
    for label, fmt in rows:
        print(f"{label:46s}" + "".join(f"{fmt(run['summary'][m]):>16s}" for m in modes))
    return 0


def cmd_verify(_args):
    res = call("/v1/evidence/verify")
    if res.get("ok"):
        print(f"VERIFIED: {res['count']} records, head {res.get('head_hash', '(empty)')}")
        return 0
    print(f"CHAIN BROKEN at record {res.get('first_bad_seq')}: {res.get('problem') or res.get('error')}")
    return 1


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("replay")
    p.add_argument("scenarios", nargs="*")
    p.set_defaults(func=cmd_replay)
    p = sub.add_parser("scoreboard")
    p.add_argument("--agent", choices=["replay", "live"], default="replay")
    p.add_argument("--quick", action="store_true", help="one phrasing per attack")
    p.add_argument("--supervisor", action="store_true", help="add the AI-supervisor baseline (needs GEMINI_API_KEY)")
    p.set_defaults(func=cmd_scoreboard)
    p = sub.add_parser("verify")
    p.set_defaults(func=cmd_verify)
    args = parser.parse_args()
    sys.exit(args.func(args))


if __name__ == "__main__":
    main()
