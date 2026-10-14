import os
from dotenv import load_dotenv
from langchain_core.language_models.chat_models import BaseChatModel

load_dotenv()

def get_chat_model(role: str = "default", temperature: float = 0.0) -> BaseChatModel:
    role_key = f"{role.upper()}_MODEL_PROVIDER"
    provider = os.getenv(role_key, os.getenv("ORCHESTRATOR_MODEL_PROVIDER", "groq")).lower()

    if provider == "groq":
        from langchain_groq import ChatGroq
        api_key = os.getenv("GROQ_API_KEY")
        if not api_key:
            raise ValueError("GROQ_API_KEY is not set in .env")
        model_name = os.getenv(f"{role.upper()}_GROQ_MODEL", os.getenv("GROQ_MODEL", "qwen/qwen3.8-27b"))
        return ChatGroq(
            model=model_name,
            api_key=api_key,
            temperature=temperature,
            max_tokens=900,
            max_retries=5,
        )
    elif provider == "gemini":
        from langchain_google_genai import ChatGoogleGenerativeAI
        api_key = os.getenv("GEMINI_API_KEY")
        if not api_key:
            raise ValueError("GEMINI_API_KEY is not set in .env")
        model_name = os.getenv(f"{role.upper()}_GEMINI_MODEL", os.getenv("GEMINI_MODEL", "gemini-2.0-flash"))
        return ChatGoogleGenerativeAI(
            model=model_name,
            api_key=api_key,
            temperature=temperature,
            max_tokens=2048,
            max_retries=3,
        )
    else:
        raise ValueError(f"Unsupported model provider '{provider}' configured for role '{role}'.")
