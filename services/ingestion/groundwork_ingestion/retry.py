from __future__ import annotations

from collections.abc import Callable
from random import random
from time import sleep
from typing import TypeVar

T = TypeVar("T")


def retry_with_backoff(operation: Callable[[], T], *, attempts: int = 4, base_delay: float = 0.2, label: str = "operation") -> T:
    last_error: Exception | None = None
    for attempt in range(attempts):
        try:
            return operation()
        except Exception as exc:  # noqa: BLE001
            last_error = exc
            if attempt == attempts - 1:
                break
            sleep(base_delay * (2**attempt) + random() * base_delay)
    raise TimeoutError(f"{label} failed after {attempts} attempts") from last_error
