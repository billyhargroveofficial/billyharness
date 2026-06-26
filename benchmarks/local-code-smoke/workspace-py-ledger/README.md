# Python Ledger Smoke Task

Implement `ledger.py`.

Required API:

- `parse_amount(value)`: convert strings like `"$1,234.50"`, `"-12.00"`, and numbers to integer cents.
- `format_amount(cents)`: convert integer cents to a string like `"$1,234.50"` or `"-$12.00"`.
- `Ledger`: class with `add(description, amount)`, `balance()`, and `by_description()`.

Rules:

- Store amounts as integer cents.
- Preserve insertion order for `by_description()`.
- Reject empty descriptions.
- Use only the Python standard library.

Run:

```bash
python3 test_ledger.py
```
