import os
from typing import List, Optional
from langgraph.graph import StateGraph, START, END
from langgraph.checkpoint.base import BaseCheckpointSaver
from orchestrator.state import ProjectState
from orchestrator.agents.planner import planning_node
from orchestrator.agents.frontend import frontend_node
from orchestrator.agents.backend import backend_node
from orchestrator.tools.filesystem import SafeWorkspace
from orchestrator.output import safe_relative_path


def validation_node(state: ProjectState) -> dict:
    written_files, errors = [], []
    try:
        workspace_dir = state.get("target_workspace") or os.getenv("TARGET_WORKSPACE_DIR", "./workspace_output")
        workspace = SafeWorkspace(workspace_dir)
        seen = set()
        for role in ("frontend", "backend"):
            for art in state.get(f"{role}_artifacts") or []:
                try:
                    path = safe_relative_path(art["filepath"])
                    if path in seen:
                        raise ValueError(f"Duplicate artifact path: {path}")
                    seen.add(path)
                    written_files.append(workspace.write_file(path, art["content"]))
                except Exception as exc:
                    errors.append(f"Failed to write {role} file {art.get('filepath')}: {exc}")
    except Exception as exc:
        errors.append(f"Workspace error: {exc}")
    failed = bool(errors or state.get("errors")) or any(
        state.get(f"{role}_status") == "failed" for role in ("frontend", "backend")
    )
    return {
        "validation_status": "failed" if failed else "completed",
        "test_results": {"written_files": written_files, "total_files": len(written_files)},
        "errors": errors,  # Delta only: errors is an operator.add channel.
        "overall_status": "failed_with_errors" if failed else "success",
    }


def route_after_planner(state: ProjectState) -> List[str]:
    if state.get("planning_status") == "failed":
        return [END]

    targets = []
    if bool(state.get("frontend_tasks")):
        targets.append("frontend")
    if bool(state.get("backend_tasks")):
        targets.append("backend")

    return targets if targets else ["validation"]


def build_orchestrator_graph(checkpointer: Optional[BaseCheckpointSaver] = None):
    builder = StateGraph(ProjectState)

    builder.add_node("planner", planning_node)
    builder.add_node("frontend", frontend_node)
    builder.add_node("backend", backend_node)
    builder.add_node("validation", validation_node)

    builder.add_edge(START, "planner")

    # Conditional fan-out to parallel workers
    builder.add_conditional_edges(
        "planner",
        route_after_planner,
        ["frontend", "backend", "validation", END]
    )

    # Fan-in convergence into validation
    builder.add_edge("frontend", "validation")
    builder.add_edge("backend", "validation")
    builder.add_edge("validation", END)

    return builder.compile(checkpointer=checkpointer)
