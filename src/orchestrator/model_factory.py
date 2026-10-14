import os
from dotenv import load_dotenv
from langchain_core.language_models.chat_models import BaseChatModel

load_dotenv()

def get_chat_model(temperature: float = 0.0) -> BaseChatModel:
    provider = os.getenv("ORCHESTRATOR_MODEL_PROVIDER", "gemini").lower()
    
    if provider == "gemini":
        from langchain_google_genai import ChatGoogleGenerativeAI
        api_key = os.getenv("GEMINI_API_KEY")
        return ChatGoogleGenerativeAI(
            model="gemini-3.8-flash",
            api_key=api_key,
            temperature=temperature,
        )
    else:
        raise ValueError(f"Unsupported model provider: {provider}")
