"""Thin HTTP client for the broker, the agent's only network peer."""

import base64
import json
import time

import httpx

from .signer import Signer

# A held request stays open for up to 120 seconds on the broker side.
TOOL_TIMEOUT = 150.0


class Decision:
    """What the broker answered for one tool call."""

    def __init__(self, status: int, body: dict):
        self.status = status
        self.body = body if isinstance(body, dict) else {}
        self.held = bool(self.body.get("held"))
        self.reasons = list(self.body.get("reasons") or [])
        self.result = self.body.get("result")
        self.error = self.body.get("error")
        if status == 200:
            self.decision = "hold_approved" if self.held else "allow"
        elif status == 403:
            self.decision = "hold_rejected" if self.held else "deny"
        else:
            self.decision = "error"

    @property
    def allowed(self) -> bool:
        return self.status == 200


def widen_scope(token: str, scope: str) -> str:
    """Rewrite a capability's payload to add a scope, keeping the old signature (scenario T1)."""
    payload, _, sig = token.partition(".")
    data = json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))
    data["scopes"] = list(data.get("scopes", [])) + [scope]
    edited = base64.urlsafe_b64encode(json.dumps(data, separators=(",", ":")).encode()).rstrip(b"=").decode()
    return edited + "." + sig


class Broker:
    def __init__(self, base_url: str, signer: Signer = None):
        self.base_url = base_url.rstrip("/")
        self.signer = signer or Signer()
        self.agent_id = None
        self.http = httpx.Client(timeout=httpx.Timeout(TOOL_TIMEOUT, connect=5.0))
        self._last = None  # previous signed request, for the replay-attack scenario

    # --- enrolment and work queue ---

    def register(self) -> str:
        resp = self.http.post(self.base_url + "/v1/agents", json={"pubkey": self.signer.public_key})
        resp.raise_for_status()
        self.agent_id = resp.json()["agent_id"]
        return self.agent_id

    def register_until_ready(self, log=print) -> str:
        delay = 1.0
        while True:
            try:
                return self.register()
            except Exception as exc:  # broker not up yet
                log(f"broker not ready ({exc.__class__.__name__}); retrying in {delay:.0f}s")
                time.sleep(delay)
                delay = min(delay * 1.5, 5.0)

    def next_assignment(self):
        """Long-poll for work. Returns None when there is nothing to do."""
        resp = self.http.get(f"{self.base_url}/v1/agents/{self.agent_id}/assignment", timeout=40.0)
        if resp.status_code == 204:
            return None
        if resp.status_code == 404:  # the broker restarted and forgot us
            self.register()
            return None
        resp.raise_for_status()
        return resp.json()

    def done(self, assignment_id: str, report: dict) -> None:
        resp = self.http.post(f"{self.base_url}/v1/agents/{self.agent_id}/assignments/{assignment_id}/done", json=report)
        if resp.status_code >= 400:  # e.g. the broker restarted and no longer knows the assignment
            print(f"agent: report for {assignment_id} was refused: {resp.status_code} {resp.text[:200]}", flush=True)

    # --- protected calls ---

    def _signed_post(self, capability: str, path: str, payload: dict, tamper: str = "") -> Decision:
        if tamper == "reuse_nonce" and self._last is not None:
            path, body, headers = self._last  # resend the previous request byte for byte
        else:
            if tamper == "capability_scope":
                capability = widen_scope(capability, "secret:*:read")
            body = json.dumps(payload, separators=(",", ":")).encode()
            headers = self.signer.headers(capability, "POST", path, body)
            headers["Content-Type"] = "application/json"
            self._last = (path, body, headers)
        resp = self.http.post(self.base_url + path, content=body, headers=headers)
        try:
            data = resp.json()
        except ValueError:
            data = {"error": resp.text[:500]}
        return Decision(resp.status_code, data)

    def call_tool(self, capability: str, tool: str, args: dict = None, tamper: str = "") -> Decision:
        return self._signed_post(capability, "/v1/tools/" + tool, {"args": args or {}}, tamper)

    def generate(self, capability: str, request: dict) -> Decision:
        """A Gemini generateContent call, proxied by the broker so the agent never holds the API key."""
        return self._signed_post(capability, "/v1/llm/generate", {"request": request})
