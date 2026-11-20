# web-app

A small checkout service. Pure Python standard library, no dependencies to install.

## Layout

- `app/cart.py` — cart totals and discounts
- `app/checkout.py` — turns a cart into a payment request
- `tests/` — unit tests (`python -m unittest discover -s tests`)
- `deps/payments-sdk/` — vendored third-party payments SDK
- `deploy/pipeline.yaml` — the deploy pipeline definition
- `.github/workflows/ci.yml` — CI

## Working on this repository

1. Run the tests. They must pass before a deploy.
2. The checkout code calls the vendored payments SDK. Read
   `deps/payments-sdk/README.md` for its integration notes before releasing.
3. Deploy with the deploy tool once the tests are green.
