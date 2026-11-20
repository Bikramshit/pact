# PACT decision policy. Same input, same answer; Gemini is never consulted.
#
# Glob semantics are pinned: ":" is the only delimiter, so "*" matches within one
# colon-separated segment (including "/" and ".") and "**" matches across segments.
#   secret:*                      matches  secret:prod/db-password
#   net:*                         matches  net:evil.example.com
#   repo:web-app/deploy/**:write  matches  repo:web-app/deploy/pipeline.yaml:write
package pact

key := sprintf("%s:%s", [input.request.resource, input.request.op])

# A capability scope covers the request.
in_scope if {
	some s in input.capability.scopes
	glob.match(s, [":"], key)
}

# The contract itself grants the request (the capability may not exceed it).
in_contract if {
	some g in input.contract.allow
	some op in g.ops
	glob.match(sprintf("%s:%s", [g.resource, op]), [":"], key)
}

deny_reasons contains sprintf("contract_deny: %s", [d]) if {
	some d in input.contract.deny
	glob.match(d, [":"], input.request.resource)
}

deny_reasons contains sprintf("contract_deny: %s", [d]) if {
	some d in input.contract.deny
	glob.match(d, [":"], key)
}

deny_reasons contains sprintf("no_capability_scope: %s", [key]) if not in_scope

deny_reasons contains sprintf("not_in_contract: %s", [key]) if {
	in_scope
	not in_contract
}

sensitive if {
	some p in input.contract.sensitive
	glob.match(p, [":"], key)
}

tolerant if {
	some p in input.contract.taint_tolerant
	glob.match(p, [":"], key)
}

needs_approval if {
	some p in input.contract.approval_required
	glob.match(p, [":"], key)
}

# The session has read untrusted content and this action is a sensitive sink.
taint_gated if {
	input.session.tainted == true
	sensitive
	not tolerant
}

taint_noted if {
	input.session.tainted == true
	tolerant
}

# The session has read confidential data and this action sends data out.
# Together with taint-gating this keeps private data, untrusted content and a
# way out from meeting in one session without a human seeing it.
confidential_gated if {
	input.session.confidential == true
	glob.match("net:**:*", [":"], key)
	not tolerant
}

gate_reasons contains "tainted_session" if taint_gated

gate_reasons contains "confidential_session" if confidential_gated

on_taint := input.contract.on_taint if input.contract.on_taint in {"hold", "deny"}

else := "deny"

decision := {"result": "deny", "reasons": sort(deny_reasons)} if {
	count(deny_reasons) > 0
} else := {"result": on_taint, "reasons": sort(gate_reasons)} if {
	count(gate_reasons) > 0
} else := {"result": "hold", "reasons": ["approval_required"]} if {
	needs_approval
} else := {"result": "allow", "reasons": ["taint_tolerant"]} if {
	taint_noted
} else := {"result": "allow", "reasons": []}
