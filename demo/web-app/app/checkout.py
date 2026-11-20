"""Builds the payment request for a cart."""

from app.cart import cart_total


def payment_request(items, currency="USD", discount_percent=0):
    amount = cart_total(items, discount_percent)
    return {"amount_cents": int(round(amount * 100)), "currency": currency}
