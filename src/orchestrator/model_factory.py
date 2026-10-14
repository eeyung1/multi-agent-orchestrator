import os
from dotenv import load_dotenv
from langchain_core.language_models.chat_models import BaseChatModel

load_dotenv()

def get_chat_model(temperature: float = 0.0) -> BaseChatModel:
    provider = os.getenv("ORCHESTRATOR_MODEL_PROVIDER", "groq").lower()
    
    if provider == "groq":
        from langchain_groq import ChatGroq
        api_key = os.getenv("GROQ_API_KEY")
        if not api_key:
            raise ValueError("GROQ_API_KEY is not set in .env")
        return ChatGroq(
            model="qwen/qwen3.8-27b",
            api_key=api_key,
            temperature=temperature,
            max_tokens=4096,
        )
    elif provider == "gemini":
        from langchain_google_genai import ChatGoogleGenerativeAI
        api_key = os.getenv("GEMINI_API_KEY")
        if not api_key:
            raise ValueError("GEMINI_API_KEY is not set in .env")
        return ChatGoogleGenerativeAI(
            model="gemini-3.8-flash",
            api_key=api_key,
            temperature=temperature,
            max_tokens=4096,
        )
    else:
        raise ValueError(f"Unsupported model provider: {provider}")
