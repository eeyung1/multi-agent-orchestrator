from typing import List
from pydantic import BaseModel, Field
from langchain_core.prompts import ChatPromptTemplate
from orchestrator.state import ProjectState
from orchestrator.model_factory import get_chat_model


class PlanOutput(BaseModel):
    clarified_requirements: List[str] = Field(description="List of unambiguous, actionable requirements.")
    architecture: str = Field(description="Summary of project architectural design and technology stack.")
    proposed_file_structure: List[str] = Field(description="List of relative file paths for the proposed project.")
    frontend_tasks: List[str] = Field(description="Discrete tasks required for frontend implementation.")
    backend_tasks: List[str] = Field(description="Discrete tasks required for backend implementation.")


PLANNER_PROMPT = ChatPromptTemplate.from_messages([
    ("system", (
        "You are an expert Software Architecture and Planning Agent. "
        "Analyze the project request, break down requirements, propose a clean modular architecture, "
        "and delineate frontend and backend responsibilities. Be pragmatic, modular, and precise."
    )),
    ("human", "Project Request: {request}")
])


def planning_node(state: ProjectState) -> dict:
    request = state.get("request", "").strip()
    if not request:
        return {
            "planning_status": "failed",
            "errors": state.get("errors", []) + ["No project request provided to planner."]
        }

    llm = get_chat_model(temperature=0.1)
    structured_llm = llm.with_structured_output(PlanOutput)
    chain = PLANNER_PROMPT | structured_llm

    try:
        plan: PlanOutput = chain.invoke({"request": request})
        return {
            "clarified_requirements": plan.clarified_requirements,
            "architecture": plan.architecture,
            "proposed_file_structure": plan.proposed_file_structure,
            "frontend_tasks": plan.frontend_tasks,
            "backend_tasks": plan.backend_tasks,
            "planning_status": "completed",
            "overall_status": "planned"
        }
    except Exception as e:
        return {
            "planning_status": "failed",
            "errors": state.get("errors", []) + [f"Planning error: {str(e)}"]
        }
