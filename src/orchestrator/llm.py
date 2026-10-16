"""Provider-independent invocation, bounded backoff, and text normalization."""
import random
import re
import time
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime

from orchestrator.model_factory import number_setting


def retryable(exc):
    response = getattr(exc, "response", None)
    status = getattr(exc, "status_code", None) or getattr(response, "status_code", None)
    code = getattr(exc, "code", None)
    if callable(code):
        code = code()
    text = f"{type(exc).__name__} {exc} {code}".lower()
    return status in (429, 500, 502, 503, 504) or any(marker in text for marker in (
        "429", "resource_exhausted", "resource exhausted", "ratelimit", "rate_limit",
        "rate limit", "too many requests", "tokens per minute", "requests per minute", "tpm", "rpm",
    )) or isinstance(exc, (TimeoutError, ConnectionError))


def retry_after(exc):
    headers = getattr(getattr(exc, "response", None), "headers", {}) or {}
    value = headers.get("retry-after") or headers.get("Retry-After")
    if value is None:
        return 0
    try:
        return max(0, float(value))
    except ValueError:
        try:
            return max(0, (parsedate_to_datetime(value) - datetime.now(timezone.utc)).total_seconds())
        except (ValueError, TypeError):
            return 0


def invoke_text(model, messages, role):
    attempts = number_setting(role, "LLM_MAX_ATTEMPTS", 4, integer=True, minimum=1)
    base = number_setting(role, "LLM_RETRY_BASE_SECONDS", 1)
    cap = number_setting(role, "LLM_RETRY_MAX_SECONDS", 60)
    for attempt in range(attempts):
        try:
            response = model.invoke(messages)
            break
        except Exception as exc:
            if not retryable(exc) or attempt == attempts - 1:
                raise
            delay = min(cap, max(retry_after(exc), base * 2 ** attempt + random.uniform(0, base)))
            time.sleep(delay)
    metadata = getattr(response, "response_metadata", {}) or {}
    reason = str(metadata.get("finish_reason", metadata.get("stop_reason", metadata.get("done_reason", "")))).lower()
    reason = reason.rsplit(".", 1)[-1]
    if reason in ("length", "max_tokens", "max_token", "max_output_tokens"):
        raise ValueError("Model output was truncated; raise MAX_TOKENS or request smaller files/tasks.")
    content = response if isinstance(response, str) else response.content
    if isinstance(content, list):
        content = "\n".join(block if isinstance(block, str) else block.get("text", "")
                            for block in content if isinstance(block, (dict, str)))
    if not isinstance(content, str):
        raise ValueError("Model did not return text")
    content = re.sub(r"<think>.*?</think>", "", content, flags=re.S).strip()
    if not content:
        raise ValueError("Model returned empty text")
    return content
