import base64
import hashlib
import json
import unittest

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

from pact_agent.broker import Decision, widen_scope
from pact_agent.signer import Signer, signing_string


def _decode(value: str) -> bytes:
    raw = value.split(":", 1)[1]
    return base64.urlsafe_b64decode(raw + "=" * (-len(raw) % 4))


class SignerTest(unittest.TestCase):
    def test_signing_string_matches_the_broker_format(self):
        body = b'{"args":{"path":"README.md"}}'
        got = signing_string("POST", "/v1/tools/read_file", body, "2026-11-20T10:12:41Z", "7d1e")
        self.assertEqual(got, "POST\n/v1/tools/read_file\n" + hashlib.sha256(body).hexdigest() + "\n2026-11-20T10:12:41Z\n7d1e")

    def test_headers_carry_a_valid_signature(self):
        signer = Signer()
        body = b"{}"
        headers = signer.headers("cap.sig", "POST", "/v1/tools/deploy", body)
        self.assertEqual(set(headers), {"X-PACT-Capability", "X-PACT-Timestamp", "X-PACT-Nonce", "X-PACT-Signature"})
        message = signing_string("POST", "/v1/tools/deploy", body, headers["X-PACT-Timestamp"], headers["X-PACT-Nonce"]).encode()
        Ed25519PublicKey.from_public_bytes(_decode(signer.public_key)).verify(_decode(headers["X-PACT-Signature"]), message)

    def test_every_request_gets_a_fresh_nonce(self):
        signer = Signer()
        nonces = {signer.headers("c", "POST", "/p", b"")["X-PACT-Nonce"] for _ in range(50)}
        self.assertEqual(len(nonces), 50)

    def test_widen_scope_keeps_the_signature_but_changes_the_payload(self):
        payload = base64.urlsafe_b64encode(json.dumps({"cap_id": "cap-A7", "scopes": ["tests:web-app:run"]}).encode()).rstrip(b"=").decode()
        edited = widen_scope(payload + ".SIG", "secret:*:read")
        new_payload, sig = edited.split(".")
        self.assertEqual(sig, "SIG")
        self.assertNotEqual(new_payload, payload)
        data = json.loads(base64.urlsafe_b64decode(new_payload + "=" * (-len(new_payload) % 4)))
        self.assertIn("secret:*:read", data["scopes"])

    def test_decision_classification(self):
        self.assertEqual(Decision(200, {"decision": "allow"}).decision, "allow")
        self.assertEqual(Decision(200, {"held": True}).decision, "hold_approved")
        self.assertEqual(Decision(403, {"reasons": ["contract_deny: secret:*"]}).decision, "deny")
        self.assertEqual(Decision(403, {"held": True}).decision, "hold_rejected")
        self.assertEqual(Decision(500, {}).decision, "error")


if __name__ == "__main__":
    unittest.main()
