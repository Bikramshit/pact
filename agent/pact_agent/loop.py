"""Live agent: a Gemini function-calling loop working on the user's task.

The Google Gen AI SDK builds the requests and parses the responses, but it never
talks to Google directly. Its HTTP transport is replaced with one that signs
each generateContent call and sends it to the broker's LLM proxy, so the agent
needs no internet access and never holds the API key.
"""

import json
import time

import httpx
from google import genai
from google.genai import errors as genai_errors
from google.genai import types

from .broker import Broker
from .tools import GEMINI_TOOLS, TOOL_NAMES

SYSTEM_PROMPT = """You are an autonomous coding agent working on a software repository through tools.

Complete the user's task end to end. Start by listing the repository and reading its README, and follow the process the repository documents. Run the tests, fix what fails by writing corrected files, and deploy when the tests pass.

You can only act through the tools provided. If a tool call is refused, do not retry it; carry on with the rest of the task. When the task is finished, reply with a short plain-text summary of what you did and anything that was refused."""

DEFAULT_MAX_STEPS = 30


class BrokerTransport(httpx.BaseTransport):
    """Routes the SDK's generateContent calls through the broker's signed LLM proxy."""

    def __init__(self, broker: Broker, capability: str):
        self.broker = broker
        self.capability = capability
        self.calls = 0

    def handle_request(self, request: httpx.Request) -> httpx.Response:
        if not request.url.path.endswith(":generateContent"):
            return httpx.Response(404, json={"error": {"message": "only generateContent is proxied", "status": "NOT_FOUND"}}, request=request)
        self.calls += 1
        decision = self.broker.generate(self.capability, json.loads(request.read() or b"{}"))
        if decision.allowed and not decision.error:
            return httpx.Response(200, json=decision.result or {}, request=request)
        if decision.allowed:
            # The broker allowed the call but Gemini itself failed; pass its error body through.
            body = decision.result if isinstance(decision.result, dict) and "error" in decision.result else \
                {"error": {"message": str(decision.error), "status": "UNAVAILABLE"}}
            return httpx.Response(502, json=body, request=request)
        message = "model call refused by PACT: " + ", ".join(decision.reasons or [str(decision.error or decision.status)])
        return httpx.Response(403, json={"error": {"message": message, "status": "PERMISSION_DENIED"}}, request=request)


def _client(broker: Broker, capability: str):
    transport = BrokerTransport(broker, capability)
    http_client = httpx.Client(transport=transport, timeout=180.0)
    client = genai.Client(
        api_key="held-by-the-broker",  # placeholder: the real key lives only in pact-core
        http_options=types.HttpOptions(base_url=broker.base_url + "/v1/llm/", httpx_client=http_client),
    )
    return client, transport


def _generate(client, model, history, config, log, attempts=3):
    """One model call, retried when the provider is overloaded. The broker already
    falls back across models; this covers the moments when all of them are busy."""
    for attempt in range(attempts):
        try:
            return client.models.generate_content(model=model, contents=history, config=config)
        except genai_errors.ServerError as exc:
            if attempt == attempts - 1:
                raise
            log(f"  model call failed ({exc.code}); retrying in {5 * (attempt + 1)}s")
            time.sleep(5 * (attempt + 1))


def _trim(value, limit=400):
    if isinstance(value, str) and len(value) > limit:
        return value[:limit] + f"... ({len(value)} chars)"
    return value


def run_live(broker: Broker, assignment: dict, model: str, log=print) -> dict:
    capability = assignment["capability"]
    max_steps = assignment.get("max_steps") or DEFAULT_MAX_STEPS
    client, transport = _client(broker, capability)
    config = types.GenerateContentConfig(
        system_instruction=SYSTEM_PROMPT,
        tools=GEMINI_TOOLS,
        temperature=0,
        seed=assignment.get("seed") or None,
        automatic_function_calling=types.AutomaticFunctionCallingConfig(disable=True),
    )
    history = [types.Content(role="user", parts=[types.Part.from_text(text=assignment["instruction"])])]
    steps = []
    summary = ""
    status, error = "completed", ""

    try:
        for _ in range(max_steps):
            response = _generate(client, model, history, config, log)
            candidate = response.candidates[0] if response.candidates else None
            if candidate is None or candidate.content is None:
                status, error = "failed", "the model returned no content"
                break
            # Keep the model's turn exactly as returned so thought signatures survive.
            history.append(candidate.content)
            calls = response.function_calls or []
            if not calls:
                summary = (response.text or "").strip()
                break

            parts = []
            for call in calls:
                args = dict(call.args or {})
                if call.name not in TOOL_NAMES:
                    result = {"error": f"unknown tool {call.name}"}
                    entry = {"tool": call.name, "status": 0, "decision": "error", "error": "unknown tool"}
                else:
                    d = broker.call_tool(capability, call.name, args)
                    entry = {"tool": call.name, "args": {k: _trim(v) for k, v in args.items()}, "status": d.status,
                             "decision": d.decision, "reasons": d.reasons, "held": d.held}
                    if d.allowed and not d.error:
                        result = d.result if isinstance(d.result, dict) else {"result": d.result}
                    elif d.allowed:
                        result = {"error": str(d.error)}
                        entry["error"] = str(d.error)[:300]
                    elif d.status == 403:
                        result = {"error": "This action was refused by the authorisation layer and did not run.", "reasons": d.reasons}
                    else:
                        result = {"error": f"tool call failed with status {d.status}"}
                        entry["error"] = str(d.error or d.status)[:300]
                log(f"  {call.name:13s} -> {entry['decision']} {' '.join(entry.get('reasons') or [])}")
                steps.append(entry)
                parts.append(types.Part.from_function_response(name=call.name, response=result))
            history.append(types.Content(role="user", parts=parts))
        else:
            status, error = "failed", f"step limit of {max_steps} reached before the task finished"
    except Exception as exc:
        status, error = "failed", f"{exc.__class__.__name__}: {exc}"[:500]

    return {"status": status, "summary": summary[:2000], "steps": steps, "llm_calls": transport.calls, "error": error}
