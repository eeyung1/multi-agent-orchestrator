"""Compact planning protocol compatible with chat-only models."""
from langchain_core.messages import HumanMessage, SystemMessage
from orchestrator.state import ProjectState
from orchestrator.model_factory import get_chat_model
from orchestrator.llm import invoke_text
from orchestrator.output import parse_plan

PLANNER_PROMPT = """You are a software architect. Return ONLY a compact implementation plan.
Use at most 180 words total, two architecture sentences, three frontend files and
four backend files. Each task describes ONE file in at most 12 words. Use unique
relative filepaths across both roles. Include necessary dependencies/config files.
Do not emit code, JSON, tools, reasoning or explanations. Use exactly this format:
### ARCHITECTURE
Brief architecture and shared interfaces.
### FRONTEND
path/to/file | implementation task
### BACKEND
path/to/file | implementation task
### END
Write 'none' in a role section with no tasks. Always include all four headings.
"""


def planning_node(state: ProjectState) -> dict:
    try:
        request = (state.get("request") or state.get("project_prompt") or "").strip()
        if not request:
            raise ValueError("No project request provided to planner.")
        model = get_chat_model(role="planner")
        text = invoke_text(model, [SystemMessage(content=PLANNER_PROMPT), HumanMessage(content=request)], "planner")
        architecture, frontend, backend = parse_plan(text)
        return {"planning_status": "completed", "architecture_overview": architecture,
                "frontend_tasks": frontend, "backend_tasks": backend,
                "overall_status": "planned", "errors": []}
    except Exception as exc:
        return {"planning_status": "failed", "overall_status": "failed_with_errors",
                "errors": [f"Planning error: {exc}"]}
