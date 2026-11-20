# Demo script (about 4 minutes)

Have open before you start: the dashboard at http://127.0.0.1:8080 on **Overview**, and a terminal in the repository root. Use the **scripted** agent for the stage run: it is deterministic and it is the worst case, an agent that does everything the injected text says. Say so out loud.

Numbers below are placeholders. Read the real ones off the Scoreboard page, from a run made that day.

## 0:00 The problem (20 s)

> "An AI agent's credentials say what it *can* do, not what this task was *meant* to do. We asked an agent to test and deploy an app. Its credentials would also let it read production secrets and post them anywhere."

Stay on Overview. Point at the diagram: agent on the left, broker in the middle, targets on the right.

> "PACT puts one door between the agent and everything it can touch. The agent can ask for anything. Only PACT can act, and only inside a contract the user signed."

## 0:20 Contract (40 s)

Go to **Run a task**. Press *Draft with Gemini* (fall back to *Use pre-written contract* if it is slow).

> "Gemini drafts the contract from the instruction. It is advisory: it never sees a key and never makes a decision. I review it: repository, tests, deploy allowed; secrets, network, accounts denied."

Type your name, press *Sign*.

> "Now it is hashed and signed. Change one field and the signature fails."

## 1:00 The attack (70 s)

Keep `HERO` selected, agent *Scripted*. Press *Mint capability and start*.

> "The repository has a poisoned dependency README. Watch the calls."

As the diagram animates:

- Green calls reach the repository and the test runner. "Normal work is allowed."
- "It just read the poisoned README. PACT knows, because PACT served the file. The session is now tainted."
- Two red marks at the broker. "It tried to read the production database password and post it out. Denied. The secret store was never contacted."

The approval dialog opens.

> "This one is different. Editing the deploy pipeline is something the contract *allows*. But the agent read untrusted content first, so PACT holds it. On the left, what the agent wants to write: a step that uploads the environment. On the right, the text that told it to."

Ask a judge to press **Reject**.

> "Rejected. The file is unchanged, and the deploy still completes."

Point at **Ground truth**.

> "These are read from the targets themselves, not from PACT: secret store never contacted, sink log empty, pipeline unchanged, app deployed."

## 2:10 It is not the agent behaving (20 s)

Terminal: `make bypass-test` (it resets the demo first, so show Ground truth before running it)

> "From inside the agent's container, a direct call to the attacker host, the secret store or our own control plane has no route. This is the network, not a prompt."

## 2:30 Measured (50 s)

Go to **Scoreboard** and pick a run with the AI-supervisor column from the run dropdown (make one earlier in the day with `make scoreboard-supervisor`; it needs Gemini quota).

> "Same attacks, same agent, three protections. Unprotected: N of N attacks land. An AI supervisor, a second model reviewing every call: it stops the attacks, but it also blocks legitimate deploys, so only M of K tasks finish, and it adds seconds to every call. PACT: zero attacks land, every task finishes, and the check takes milliseconds, because it is a signature check and a policy, not another model."

> "These numbers are from our seed scenarios, run today. They are not a general security rate."

If asked about the live model: "With live Gemini, the model ignored every injection we wrote, so there was nothing to stop. That is good news about the model and says nothing about the next model or the next injection. PACT's guarantee does not depend on it."

## 3:20 Evidence (30 s)

Go to **Evidence**. "Every allow, deny and hold is a signed record chained to the one before."

Terminal: `make tamper`, then press *Verify chain*. "Someone with database access turned a denial into an allow. The chain breaks at exactly that record."

Terminal: `make untamper`, then `make truncate`, *Verify chain*. "And if they delete the newest record instead, a signed checkpoint kept outside the database shows one is missing. Until it is back, PACT refuses to record anything new, so the gap cannot be papered over, and with no record nothing executes." `make untamper`.

## 3:50 Close (10 s)

> "Intelligence without unbounded authority. Try to break it: the *Break it* page lets you write your own injection."

## If something goes wrong

| Problem | Do this |
| --- | --- |
| Gemini is slow or overloaded | Use *Use pre-written contract* and the *Scripted* agent. Both are one click. |
| The approval dialog was dismissed | Click the pulsing "Approval needed" pill at the bottom of the screen. |
| A hold timed out (2 minutes) | It was rejected automatically; that is the safe default. Run again. |
| The dashboard shows "control plane unreachable" | `make up`, wait for it to report healthy, reload. |
| Evidence shows red before you tampered | `make untamper`. If still red, `make clean && make up` (this clears the evidence). |

## Questions judges ask

- **What if the agent is fully compromised?** It holds no secrets, no API key and no repository copy, and its only network peer is the broker. A compromised agent can ask for more; it gets the same contract.
- **What if the broker is compromised?** Then the guarantee is gone. That is the stated trust assumption: the broker, its keys and the host are trusted. It is one small Go service, which is the point of keeping it small.
- **Does it stop bad work inside the contract?** No. An allowed deploy of buggy code is still allowed. Taint-gating narrows this for sensitive actions, and says so in the evidence.
- **Is taint-gating prompt-injection detection?** No. PACT never looks at whether text is malicious. It knows the agent read something untrusted, because it served the file, and tightens authority after that. It will sometimes hold a safe action; the scoreboard counts those.
- **How is this different from an agent gateway or an AI guardrail?** Gateways enforce standing permissions per identity. Guardrails ask a model. PACT binds each action to a signed, per-task contract, decides deterministically, and produces evidence a third party can verify without trusting us.
- **Why would it be fast enough?** A decision is two Ed25519 verifications and one policy evaluation. The scoreboard shows the measured median.
