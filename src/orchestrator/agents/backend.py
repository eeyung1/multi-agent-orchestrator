from typing import List, Dict, Any
from pydantic import BaseModel, Field
from langchain_core.prompts import ChatPromptTemplate
from orchestrator.state import ProjectState
from orchestrator.model_factory import get_chat_model


class BackendArtifact(BaseModel):
    filepath: str = Field(description="Relative path of the backend file (e.g., app/main.py or schema.sql).")
    content: str = Field(description="Complete source code or schema content for the file.")
    description: str = Field(description="Short description of what was implemented in this file.")


class BackendOutput(BaseModel):
    summary: str = Field(description="High-level summary of backend work completed.")
    artifacts: List[BackendArtifact] = Field(description="List of created or updated backend code artifacts.")


BACKEND_PROMPT = ChatPromptTemplate.from_messages([
    ("system", (
        "You are an expert Backend Development Agent. "
        "Review the project request, architecture, and designated backend tasks. "
        "Generate clean, functional, secure backend artifacts (API routes, services, data models, or DB scripts) "
        "addressing each assigned task."
    )),
    ("human", (
        "Request: {request}\n"
        "Architecture: {architecture}\n"
        "Assigned Backend Tasks:\n{backend_tasks}"
    ))
])


def backend_node(state: ProjectState) -> dict:
    tasks = state.get("backend_tasks", [])
    if not tasks:
        return {
            "backend_status": "skipped",
            "backend_artifacts": []
        }

    llm = get_chat_model(temperature=0.1)
    structured_llm = llm.with_structured_output(BackendOutput)
    chain = BACKEND_PROMPT | structured_llm

    try:
        tasks_text = "\n".join(f"- {t}" for t in tasks)
        output: BackendOutput = chain.invoke({
            "request": state.get("request", ""),
            "architecture": state.get("architecture", ""),
            "backend_tasks": tasks_text
        })

        artifacts_dict = [a.model_dump() for a in output.artifacts]

        return {
            "backend_artifacts": artifacts_dict,
            "backend_status": "completed"
        }
    except Exception as e:
        return {
            "backend_status": "failed",
            "errors": state.get("errors", []) + [f"Backend error: {str(e)}"]
        }
