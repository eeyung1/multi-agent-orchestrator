from typing import List
from pydantic import BaseModel, Field
from langchain_core.prompts import ChatPromptTemplate
from orchestrator.state import ProjectState
from orchestrator.model_factory import get_chat_model

class PlanOutput(BaseModel):
    clarified_requirements: List[str] = Field(description="Max 4 concise key requirements.")
    architecture: str = Field(description="Brief 2-3 sentence overview of architecture and components.")
    proposed_file_structure: List[str] = Field(description="List of relative file paths to create.")
    frontend_tasks: List[str] = Field(description="List of concise frontend tasks (1 line each).")
    backend_tasks: List[str] = Field(description="List of concise backend tasks (1 line each).")

PLANNER_PROMPT = ChatPromptTemplate.from_messages([
    ("system", (
        "You are an expert Software Architect. Analyze the project request and produce a modular development plan.\n"
        "CRITICAL INSTRUCTIONS:\n"
        "- Keep every task description strictly under 15 words.\n"
        "- Limit clarified_requirements to at most 4 brief bullet points.\n"
        "- Limit architecture to at most 2 sentences.\n"
        "- Limit frontend_tasks to at most 3 items.\n"
        "- Limit backend_tasks to at most 4 items.\n"
        "- Avoid long explanations or character-by-character breakdowns."
    )),
    ("human", "Project Request:\n{request}")
])

def planning_node(state: ProjectState) -> dict:
    req_val = state.get("request") or state.get("project_prompt") or ""
    request = req_val.strip()
    if not request:
        return {
            "planning_status": "failed",
            "errors": ["No project request provided to planner."]
        }

    llm = get_chat_model(role="planner", temperature=0.1)
    structured_llm = llm.with_structured_output(PlanOutput)

    try:
        chain = PLANNER_PROMPT | structured_llm
        output: PlanOutput = chain.invoke({"request": request})

        return {
            "planning_status": "completed",
            "architecture_overview": output.architecture,
            "frontend_tasks": output.frontend_tasks,
            "backend_tasks": output.backend_tasks,
            "errors": []
        }
    except Exception as e:
        return {
            "planning_status": "failed",
            "errors": [f"Planning error: {str(e)}"]
        }
