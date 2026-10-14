from typing import List, Dict, Any
from pydantic import BaseModel, Field
from langchain_core.prompts import ChatPromptTemplate
from orchestrator.state import ProjectState
from orchestrator.model_factory import get_chat_model


class FrontendArtifact(BaseModel):
    filepath: str = Field(description="Relative path of the frontend file (e.g., templates/index.html).")
    content: str = Field(description="Complete source code or markup for the file.")
    description: str = Field(description="Short description of what was implemented in this file.")


class FrontendOutput(BaseModel):
    summary: str = Field(description="High-level summary of frontend work completed.")
    artifacts: List[FrontendArtifact] = Field(description="List of created or updated frontend code artifacts.")


FRONTEND_PROMPT = ChatPromptTemplate.from_messages([
    ("system", (
        "You are an expert Frontend Development Agent. "
        "Review the project request, architecture, and designated frontend tasks. "
        "Generate clean, functional, modern frontend artifacts (HTML, CSS, JS, or client components) "
        "addressing each assigned task."
    )),
    ("human", (
        "Request: {request}\n"
        "Architecture: {architecture}\n"
        "Assigned Frontend Tasks:\n{frontend_tasks}"
    ))
])


def frontend_node(state: ProjectState) -> dict:
    tasks = state.get("frontend_tasks", [])
    if not tasks:
        return {
            "frontend_status": "skipped",
            "frontend_artifacts": []
        }

    llm = get_chat_model(role="frontend", temperature=0.1)
    structured_llm = llm.with_structured_output(FrontendOutput)
    chain = FRONTEND_PROMPT | structured_llm

    try:
        tasks_text = "\n".join(f"- {t}" for t in tasks)
        output: FrontendOutput = chain.invoke({
            "request": state.get("request", ""),
            "architecture": state.get("architecture", ""),
            "frontend_tasks": tasks_text
        })

        artifacts_dict = [a.model_dump() for a in output.artifacts]

        return {
            "frontend_artifacts": artifacts_dict,
            "frontend_status": "completed"
        }
    except Exception as e:
        return {
            "frontend_status": "failed",
            "errors": state.get("errors", []) + [f"Frontend error: {str(e)}"]
        }
