def raises(exc_type, func):
    try:
        func()
    except exc_type:
        return
    except Exception as exc:
        raise AssertionError(f"expected {exc_type.__name__}, got {type(exc).__name__}") from exc
    raise AssertionError(f"expected {exc_type.__name__}")
