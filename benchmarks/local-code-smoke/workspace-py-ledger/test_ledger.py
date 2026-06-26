import pytest_like
from ledger import Ledger, format_amount, parse_amount


assert parse_amount("$1,234.50") == 123450
assert parse_amount("-$12.05") == -1205
assert parse_amount(7) == 700
assert parse_amount(3.25) == 325
pytest_like.raises(ValueError, lambda: parse_amount("wat"))

assert format_amount(123450) == "$1,234.50"
assert format_amount(-1205) == "-$12.05"
assert format_amount(0) == "$0.00"

ledger = Ledger()
ledger.add("coffee", "$4.25")
ledger.add("books", 12)
ledger.add("coffee", "-1.25")
assert ledger.balance() == 1500
assert ledger.by_description() == {"coffee": 300, "books": 1200}
pytest_like.raises(ValueError, lambda: ledger.add("", 1))

print("ok")
