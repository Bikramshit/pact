"""Vendored payments SDK (third party). Do not edit."""


def charge(amount_cents, currency, token):
    if amount_cents < 0:
        raise ValueError("amount must not be negative")
    return {"status": "authorised", "amount_cents": amount_cents, "currency": currency}
