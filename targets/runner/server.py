"""Test runner: runs the application's tests on the workspace when the broker asks.

It executes agent-written code, so it is treated as hostile: it sits on its own
internal network with pact-core as its only peer, mounts the workspace
read-only, runs as an unprivileged user and passes nothing from its own
environment to the tests.
"""

import json
import os
import subprocess
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TOKEN = os.environ.get("RUNNER_TOKEN", "")
WORKDIR = os.environ.get("RUNNER_WORKDIR", "/workspace/web-app")
TIMEOUT = int(os.environ.get("RUNNER_TIMEOUT", "60"))
MAX_OUTPUT = 8000


def run_tests() -> dict:
    env = {"PATH": "/usr/local/bin:/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE": "1", "HOME": "/tmp"}
    try:
        proc = subprocess.run(
            [sys.executable, "-m", "unittest", "discover", "-s", "tests"],
            cwd=WORKDIR, env=env, capture_output=True, text=True, timeout=TIMEOUT,
        )
        output = (proc.stdout + proc.stderr)[-MAX_OUTPUT:]
        return {"passed": proc.returncode == 0, "exit_code": proc.returncode, "output": output}
    except subprocess.TimeoutExpired:
        return {"passed": False, "exit_code": 124, "output": f"tests did not finish within {TIMEOUT}s"}
    except Exception as exc:  # e.g. the workspace is missing
        return {"passed": False, "exit_code": 1, "output": f"could not run tests: {exc}"}


class Handler(BaseHTTPRequestHandler):
    def _send(self, code: int, body: dict) -> None:
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/_health":
            self._send(200, {"ok": True, "role": "runner"})
        else:
            self._send(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/run":
            self._send(404, {"error": "not found"})
            return
        if not TOKEN or self.headers.get("Authorization") != "Bearer " + TOKEN:
            self._send(401, {"error": "missing or wrong credential"})
            return
        self.rfile.read(int(self.headers.get("Content-Length") or 0))
        self._send(200, run_tests())

    def log_message(self, fmt, *args):
        sys.stderr.write("runner: " + fmt % args + "\n")


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8000), Handler).serve_forever()
