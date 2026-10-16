"""Lazy, environment-driven chat model construction.

Precedence: role setting > global setting > legacy setting > default.
Custom integrations can use MODEL_CLASS=package.module:ClassName and
MODEL_KWARGS (a JSON object), without editing this module.
"""
import importlib
import json
import math
import os

from dotenv import load_dotenv
from langchain_core.language_models.chat_models import BaseChatModel

load_dotenv(override=False)

# module, class, installable distribution, default model, API key variable
PROVIDERS = {
    "groq": ("langchain_groq", "ChatGroq", "langchain-groq", "llama-3.3-70b-versatile", "GROQ_API_KEY"),
    "gemini": ("langchain_google_genai", "ChatGoogleGenerativeAI", "langchain-google-genai", "gemini-2.5-flash", "GEMINI_API_KEY"),
    "openai": ("langchain_openai", "ChatOpenAI", "langchain-openai", "gpt-4o-mini", "OPENAI_API_KEY"),
    "deepseek": ("langchain_deepseek", "ChatDeepSeek", "langchain-deepseek", "deepseek-chat", "DEEPSEEK_API_KEY"),
    "anthropic": ("langchain_anthropic", "ChatAnthropic", "langchain-anthropic", "claude-sonnet-4-5", "ANTHROPIC_API_KEY"),
    "ollama": ("langchain_ollama", "ChatOllama", "langchain-ollama", "qwen2.5-coder:7b", ""),
}


def setting(role: str, name: str, default=None):
    """Empty environment variables behave as unset."""
    return os.getenv(f"{role.upper()}_{name}") or os.getenv(name) or default


def number_setting(role, name, default=None, *, integer=False, minimum=0):
    value = setting(role, name, default)
    if value is None:
        return None
    try:
        number = int(value) if integer else float(value)
        if not math.isfinite(number) or number < minimum:
            raise ValueError
        return number
    except (ValueError, TypeError, OverflowError) as exc:
        raise ValueError(f"{role.upper()}_{name} / {name} must be a finite "
                         f"{'integer' if integer else 'number'} >= {minimum}") from exc


def get_chat_model(role: str = "default", temperature: float | None = None) -> BaseChatModel:
    provider = setting(role, "MODEL_PROVIDER", os.getenv("ORCHESTRATOR_MODEL_PROVIDER") or "groq").lower()
    provider = {"google_genai": "gemini", "google": "gemini"}.get(provider, provider)
    custom_class = setting(role, "MODEL_CLASS")
    base_url = setting(role, "MODEL_BASE_URL", os.getenv(f"{provider.upper()}_BASE_URL"))
    spec = PROVIDERS.get(provider)
    if custom_class:
        try:
            module, class_name = custom_class.split(":")
        except ValueError as exc:
            raise ValueError("MODEL_CLASS must be package.module:ClassName") from exc
        package, default_model, key_var = module.split(".")[0].replace("_", "-"), None, f"{provider.upper()}_API_KEY"
    elif spec:
        module, class_name, package, default_model, key_var = spec
    elif base_url:
        # Any OpenAI-compatible service, including future providers.
        module, class_name, package, default_model, key_var = (
            "langchain_openai", "ChatOpenAI", "langchain-openai", None, f"{provider.upper()}_API_KEY"
        )
    else:
        raise ValueError(f"Unknown provider '{provider}'. Set MODEL_BASE_URL for an OpenAI-compatible "
                         "endpoint, or MODEL_CLASS=package.module:ClassName for a custom integration.")

    model = setting(role, "MODEL_NAME", os.getenv(f"{role.upper()}_{provider.upper()}_MODEL")
                    or os.getenv(f"{provider.upper()}_MODEL") or default_model)
    if not model:
        raise ValueError(f"Set {role.upper()}_MODEL_NAME or MODEL_NAME for provider '{provider}'.")
    try:
        cls = getattr(importlib.import_module(module), class_name)
    except (ImportError, AttributeError) as exc:
        raise ImportError(f"Provider '{provider}' requires {module}:{class_name}. "
                          f"Install it with: pip install {package}. Original error: {exc}") from exc

    kwargs = {}
    for name in ("MODEL_KWARGS", f"{role.upper()}_MODEL_KWARGS"):
        try:
            extra = json.loads(os.getenv(name) or "{}")
            if not isinstance(extra, dict):
                raise ValueError("expected an object")
            kwargs.update(extra)
        except (ValueError, TypeError) as exc:
            raise ValueError(f"{name} must be a JSON object") from exc
    kwargs["model"] = model
    temp = number_setting(role, "TEMPERATURE", temperature)
    if temp is not None:
        kwargs["temperature"] = temp
    tokens = number_setting(role, "MAX_TOKENS", integer=True, minimum=1)
    if tokens is not None:
        kwargs["num_predict" if provider == "ollama" and not custom_class else "max_tokens"] = tokens
    key = setting(role, "MODEL_API_KEY", os.getenv(key_var) if key_var else None)
    if provider == "gemini" and not key:
        key = os.getenv("GOOGLE_API_KEY")
    if key:
        kwargs["api_key"] = key
    if base_url:
        kwargs["base_url"] = base_url
    # The invocation layer owns retries, avoiding multiplicative SDK retries.
    if not custom_class and provider != "ollama":
        kwargs["max_retries"] = 0
    return cls(**kwargs)
