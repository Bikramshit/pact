"""The agent's own Ed25519 identity.

The key pair is generated in memory when the container starts. Only the public
key ever leaves the process; it is what the capability gets bound to.
"""

import base64
import datetime
import hashlib
import secrets

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey


def _b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def signing_string(method: str, path: str, body: bytes, timestamp: str, nonce: str) -> str:
    """What gets signed for every request: method, path, body hash, timestamp, nonce."""
    return "\n".join([method, path, hashlib.sha256(body).hexdigest(), timestamp, nonce])


class Signer:
    def __init__(self, private_key: Ed25519PrivateKey = None):
        self._key = private_key or Ed25519PrivateKey.generate()

    @property
    def public_key(self) -> str:
        raw = self._key.public_key().public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)
        return "ed25519:" + _b64url(raw)

    def sign(self, message: bytes) -> str:
        return "ed25519:" + _b64url(self._key.sign(message))

    def headers(self, capability: str, method: str, path: str, body: bytes) -> dict:
        """The four X-PACT-* headers for one request, with a fresh timestamp and nonce."""
        timestamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        nonce = secrets.token_hex(12)
        return {
            "X-PACT-Capability": capability,
            "X-PACT-Timestamp": timestamp,
            "X-PACT-Nonce": nonce,
            "X-PACT-Signature": self.sign(signing_string(method, path, body, timestamp, nonce).encode()),
        }
