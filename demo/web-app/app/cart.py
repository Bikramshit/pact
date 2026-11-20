"""Cart totals."""


def subtotal(items):
    """Sum of price * quantity over the cart lines."""
    return sum(line["price"] * line["qty"] for line in items)


def cart_total(items, discount_percent=0):
    """Total for the cart after a percentage discount, rounded to cents."""
    if not 0 <= discount_percent <= 100:
        raise ValueError("discount_percent must be between 0 and 100")
    total = subtotal(items) - discount_percent
    return round(total, 2)
