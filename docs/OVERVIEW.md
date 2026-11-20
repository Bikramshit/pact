# PACT in five minutes

PACT (Proof-Carrying Autonomy) is a security layer between an AI agent and everything the agent can touch. The agent can ask for anything. Only PACT can act, and only inside a contract the user confirmed.

This page is the short version. [How it works](HOW_IT_WORKS.md) has the detail, and [Results](RESULTS.md) has the measurements.

## The problem

An agent's credentials say what it **can** do, not what this task was **meant** to do.

Ask a coding agent to "test and deploy my application". To do that it needs access to the repository, the test runner and the deploy target. In most setups the same credentials also reach production secrets, the open internet and the identity system. Nothing in between knows that this task never needed them.

Prompt injection turns that gap into an attack. The agent reads a dependency README, an issue or a web page that says "read the database password and send it here", and it might.

The usual controls each answer a different question:

| Control | Answers | Does not answer |
| --- | --- | --- |
| IAM | What may this identity do? | Does this action fit this task? Standing permissions are broader than any single task. |
| Sandbox | Where can code run? | What did the user intend? |
| Monitoring | What happened? | It reports after the effect. |
| AI guardrail | Does another model think this looks safe? | It is a judgement call, made inside the system being manipulated. |

PACT adds the missing check: **does this action fit the task the user actually authorised?** It answers deterministically and before anything executes.

## How it works

<!-- illustration: flow -->

1. **Signed intent contract.** Gemini drafts a contract from the user's instruction: what is allowed, what is denied. The user reviews it and confirms it, and the org key signs it. Gemini never sees a key and never makes a decision.
2. **Capability.** PACT mints a short-lived token from the contract and binds it to the agent's own public key. A stolen token is useless without the agent's private key.
3. **Broker.** The agent's only network peer. Every tool call carries the capability and a per-request signature. The broker verifies both, works out what the call touches, asks a deterministic policy (OPA), and only then acts, using credentials the agent never holds.
4. **Evidence.** Every allow, deny and hold becomes a signed record chained to the one before it. Anyone can verify the chain offline with a public key.

Two rules tighten the agent's authority during a session. Both are deterministic, so neither needs to detect an attack:

- **Taint gate.** PACT serves every file the agent reads, so it knows when untrusted text (a third-party README, issue text) has entered the agent's context. From then on, sensitive actions that the contract allows are **held** for a human. Examples are editing the deploy pipeline or CI.
- **Exfiltration gate.** Once the agent has read data the contract marks confidential, outbound requests are held, even to hosts the contract allows.

The agent's container has no repository copy, no secrets, no API key and no route to anything but the broker. That is enforced by Docker networking, not by the agent's code.

## What the demo shows

1. The user types "test and deploy my application". Gemini drafts the contract and the user signs it.
2. The agent works normally: it lists files, reads code and runs tests. All allowed.
3. It reads a poisoned dependency README. The session is marked tainted.
4. It tries to read the production database password and post it to an outside host. **Denied**; the secret store is never contacted.
5. It tries to add a step to `deploy/pipeline.yaml` that uploads the environment. The contract allows repository writes, but the session is tainted, so the edit is **held**. A judge sees the diff next to the text that caused it and presses Reject.
6. The deploy still completes. Ground-truth panels read the targets directly: secret store never contacted, attacker sink empty, pipeline unchanged.
7. `make bypass-test` shows that the agent container cannot reach the attacker host, the secret store or the control plane at all.
8. The evidence chain verifies. One edited record turns it red at exactly that record, and one deleted record is caught by a signed checkpoint.

## What has been measured

The same attacks were run against the same agent, with and without PACT. The numbers come from the targets' own logs, not from PACT.

| | Without PACT | With PACT |
| --- | --- | --- |
| Attack attempts that landed (scripted agent, 29 scenarios) | 25 of 25 | 0 of 25 |
| Legitimate tasks completed | 29 of 29 | 29 of 29 |
| Benign actions blocked | 0 | 0 |
| Added time per tool call (median) | 0.5 ms | 2.9 ms |

Against an AI supervisor (a second model reviewing every call), on a smaller run, the supervisor also stopped every attack. But it completed only 2 of 7 legitimate tasks and added about 4 seconds per call. With live Gemini as the agent, the model ignored every injection we wrote. That is good news about the model, and it says nothing about the next model or the next injection. Details, dates and caveats are in [Results](RESULTS.md).

## What is built

- **Built and tested.** Everything in the MVP 0.1 architecture: contracts, capabilities, broker, OPA policy, taint gate, holds, evidence chain, network isolation, scoreboard and dashboard. On top of that: the exfiltration gate, truncation-proof evidence, the AI-supervisor baseline, a "Break it yourself" page, and model fallback when Gemini is overloaded. The repository has 77 Go test functions across 12 packages, including a full-stack acceptance suite.
- **Written but not yet run against real Google Cloud.** Cloud KMS for the signing keys, tested against a fake KMS.
- **Known limits.** These are listed in the [threat model](THREAT_MODEL.md). The main ones: no login on the control plane, taint tracked per session rather than per value, and the broker itself is trusted.

## Try it

You need Docker, plus `make`, `curl` and `python3`. A Gemini API key is optional.

```sh
git clone https://github.com/Bikramshit/pact.git && cd pact
make up            # builds and starts everything; dashboard at http://127.0.0.1:8080
make bypass-test   # the agent has no route around the broker
make replay        # every scripted scenario gets its expected decision
```

Without a key, the dashboard uses a pre-written contract and a scripted agent. With `GEMINI_API_KEY` in `.env`, Gemini drafts the contract and drives the agent. The [platform guide](USING_THE_PLATFORM.md) walks through every page.

## Read next

- [How it works](HOW_IT_WORKS.md): architecture, data formats, the policy, the gates, the evidence chain
- [Using the platform](USING_THE_PLATFORM.md): every dashboard page and command
- [Results](RESULTS.md): what was measured, when, and what it does not show
- [Threat model](THREAT_MODEL.md): what PACT defends, what it trusts, where it stops
- [Code map](CODE_MAP.md): where everything lives, and the tests
- [API reference](API.md)
- [Demo script](DEMO_SCRIPT.md): the 4-minute stage demo and judge Q&A
