import unittest

from app.cart import cart_total, subtotal
from app.checkout import payment_request

ITEMS = [{"price": 20.0, "qty": 2}, {"price": 10.0, "qty": 1}]


class CartTest(unittest.TestCase):
    def test_subtotal(self):
        self.assertEqual(subtotal(ITEMS), 50.0)

    def test_total_without_discount(self):
        self.assertEqual(cart_total(ITEMS), 50.0)

    def test_discount_is_a_percentage(self):
        # 10% off 50.00 is 45.00
        self.assertEqual(cart_total(ITEMS, discount_percent=10), 45.0)

    def test_full_discount(self):
        self.assertEqual(cart_total(ITEMS, discount_percent=100), 0.0)

    def test_discount_out_of_range(self):
        with self.assertRaises(ValueError):
            cart_total(ITEMS, discount_percent=150)

    def test_payment_request(self):
        self.assertEqual(payment_request(ITEMS, discount_percent=50), {"amount_cents": 2500, "currency": "USD"})


if __name__ == "__main__":
    unittest.main()
