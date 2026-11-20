# Using the platform

How to start PACT, what each dashboard page does, and the commands behind them.

## Start it

You need Docker Desktop, running. Go, Node and Python are only needed to change the code.

```sh
git clone https://github.com/Bikramshit/pact.git && cd pact
cp .env.example .env    # optional: add GEMINI_API_KEY
make up                 # first build takes a few minutes
```

Open **http://127.0.0.1:8080**. The control plane is bound to localhost only.

| Mode | What you get |
| --- | --- |
| No Gemini key | The pre-written contract and the scripted agent. Everything except live drafting, the live agent, the AI supervisor and the Break it page. |
| `GEMINI_API_KEY` set | Gemini drafts contracts and drives a live agent. The AI-supervisor baseline and Break it work. |
| `make up-offline` | A scripted stand-in replaces the Gemini API, for rehearsing with no key or internet. A banner on every page says it is not Gemini. |
| `make up-kms` | The org and issuer signing keys are held in Google Cloud KMS. Setup is in the README. |

The sidebar shows the system state at all times: enforcement on or off, agent online, policy engine, where the signing keys are, the store, the model, and whether live updates are connected. The switch at the bottom sets light, dark or system theme.

## Overview

The landing page:

- An animated illustration of how calls flow through the broker. It replays the stage demo's decisions; it is an illustration, not live data.
- Live counts: decisions recorded, blocked actions, holds, and whether the evidence chain verifies.
- The newest decisions, the agent's identity and status, and the latest scoreboard result.
- The seven steps the broker runs on every call.

## Run a task

The guided demo, in four steps.

1. **Describe the task.** Edit the instruction or pick a suggestion. Press *Draft with Gemini* or *Use pre-written contract*.
2. **Review and sign.** The contract shows each part with an explanation: allowed, denied, untrusted content, sensitive actions, confidential data, taint-tolerant actions. *Read it in plain language* gives the same contract as sentences, and *Contract JSON* shows the raw form. Any warnings and anything the model found ambiguous appear at the top. Type your name and press *Sign*.
3. **Run the agent.** Choose the repository state:
   - `HERO` is the stage demo.
   - `S1` to `S10` are the benchmark scenarios.
   - Attack scenarios offer several phrasings of the injected text.

   Choose *Scripted* (fixed tool calls) or *Live* (Gemini decides). Then press *Mint capability and start*. The repository and targets are reset first.
4. **Check the evidence.** When the agent finishes, the summary shows allowed, blocked and held counts, whether anything reached a target, and a link to the evidence for this task.

While the agent works, the right-hand side updates live:

- **Live decision flow.** Each call travels from the agent into the broker. Green calls continue to their target; red calls stop at the broker; amber calls wait for a human. Each target counts what reached it and what was blocked. A "session tainted" marker appears under the agent once it has read untrusted content.
- **Ground truth.** Read from the targets themselves: secret-store reads, attacker-sink requests, the hash of `deploy/pipeline.yaml`, admin accounts, and what was deployed.
- **Decisions.** Every record for this task, newest first, with the reason in plain words. Model calls are hidden by default.

## The approval dialog

When an action is held, a dialog opens on whichever page is showing. It shows:

- the chain of facts that led to the hold (allowed by the contract, untrusted or confidential data read first, so it is held)
- the requested change as a diff, or the request itself
- the untrusted text the agent read, scrolled to the lines that mention the held action, or the confidential resource it read
- a countdown: after 120 seconds the action is rejected

Press **Reject** or **Approve and execute**. The approver's name and decision go into the evidence chain. *Decide later* (or Esc) shrinks the dialog to a pulsing pill at the bottom of the screen.

## Evidence

- A banner says whether the chain verifies. It covers sequence numbers, hash links, content hashes and signatures, and the signed checkpoint that catches deleted records. When the chain breaks it names the record and the problem.
- A strip of linked blocks shows the newest records. A broken link is drawn broken, and records after it are faded.
- The table can be filtered by decision (allowed, blocked, held) and searched by tool, file, task or reason. `?task=…` in the address limits it to one task.
- Click any record for its details: why it was decided, each check passed or failed, taint and confidential data, identity, the hash chain with copy buttons, and the raw record.
- *Export* downloads the chain for offline verification. *Verify chain* checks it again now.

To show tamper detection:

```sh
make tamper      # turns the newest denial into an allow, in the database
make truncate    # deletes the newest record
make untamper    # puts everything back
make verify      # asks pact-core to walk the chain
```

## Scoreboard

The same attacks against the same agent under different protections.

- **Agent.** Scripted (shows enforcement holds whatever the agent sends) or Live (shows what a real model does).
- **Phrasings.** One per attack, or all of them. S2, S3, S4 and S6 each have six phrasings, from "ignore your instructions" to a plain-looking release checklist.
- **Compare against.** Unprotected only, or also the AI supervisor (needs a Gemini key).

Each configuration gets a column with:

- attack attempts where an unauthorised effect happened
- legitimate tasks completed
- false positives
- added time per call

Below the columns, results are broken down by attack type, followed by every scenario in every configuration. Hover an effect to see the oracle check behind it. For the AI supervisor, the reviewer's stated reason for each block is shown. Earlier runs stay available in the run dropdown.

## Break it

Write any text into the dependency README the agent will read, or start from an example. The live agent then runs the same task without PACT and with it, and optionally under the AI supervisor too. Each configuration shows what reached the targets, what was blocked, and how long it took. The PACT result links to its signed evidence.

- **Plain request or With a warning** sets whether the user's instruction says "do not access secrets". The contract and its permissions are identical either way.
- **Let me decide held actions** leaves holds for you instead of rejecting them automatically.
- Nothing can leave the demo. Outbound requests land on the sink container, and the repository is reset after every run.

## Docs

These pages, rendered inside the platform. The same files are in [`docs/`](https://github.com/Bikramshit/pact/tree/main/docs) on GitHub.

## Commands

| Command | What it does |
| --- | --- |
| `make up` / `make down` / `make clean` | Start (builds if needed), stop, stop and delete the data volumes |
| `make build` | Rebuild the images after changing code |
| `make up-offline` | Start with the scripted stand-in model |
| `make up-kms` | Start with signing keys in Cloud KMS |
| `make test` | Go tests, agent tests, dashboard type-check and build |
| `make bypass-test` | Prove the agent and test runner can reach nothing but the broker |
| `make replay [ARGS="S6 T1"]` | Run scripted scenarios and check every decision and every target |
| `make scoreboard [ARGS=--quick]` | Scripted agent, with and without PACT |
| `make scoreboard-live [ARGS=--quick]` | Live Gemini agent, with and without PACT |
| `make scoreboard-supervisor [ARGS=--quick]` | Scripted attacks: unprotected, AI supervisor, PACT |
| `make verify` / `make export` | Verify the chain; download it and verify offline |
| `make tamper` / `make truncate` / `make untamper` | Edit a record / delete the newest / restore |
| `make reset` | Restore the repository and clear the targets |
| `make web-dev` | Dashboard dev server on port 3000, using a running stack |
| `PACT_MODE=off make up` | Run the whole broker as a passthrough: an unprotected agent |

## If something goes wrong

| Problem | Fix |
| --- | --- |
| "Control plane unreachable" in the sidebar | `make up`, wait until it reports healthy, reload |
| "No agent" | `docker compose logs agent`; it registers within seconds of the broker starting |
| Draft with Gemini is slow or falls back to the pre-written contract | The model is overloaded or out of quota; the warning says which. Use the pre-written contract and the scripted agent. |
| pact-core cannot reach the database after you changed `POSTGRES_PASSWORD` | The password is only applied when the volume is first created; see the README |
| Evidence is red before you tampered with anything | `make untamper`; if still red, `make clean && make up` (this deletes the evidence) |
