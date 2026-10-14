import os
from typing import List, Literal, Optional
from langgraph.graph import StateGraph, START, END
from langgraph.checkpoint.base import BaseCheckpointSaver
from orchestrator.state import ProjectState
from orchestrator.agents.planner import planning_node
from orchestrator.agents.frontend import frontend_node
from orchestrator.agents.backend import backend_node
from orchestrator.tools.filesystem import SafeWorkspace


def validation_node(state: ProjectState) -> dict:
    workspace_dir = os.getenv("TARGET_WORKSPACE_DIR", "./workspace_output")
    workspace = SafeWorkspace(workspace_dir)

    written_files = []
    errors = state.get("errors", [])

    # Write frontend artifacts
    for art in state.get("frontend_artifacts", []):
        try:
            path = workspace.write_file(art["filepath"], art["content"])
            written_files.append(path)
        except Exception as e:
            errors.append(f"Failed to write frontend file {art.get('filepath')}: {str(e)}")

    # Write backend artifacts
    for art in state.get("backend_artifacts", []):
        try:
            path = workspace.write_file(art["filepath"], art["content"])
            written_files.append(path)
        except Exception as e:
            errors.append(f"Failed to write backend file {art.get('filepath')}: {str(e)}")

    val_status = "failed" if errors else "completed"
    return {
        "validation_status": val_status,
        "test_results": {"written_files": written_files, "total_files": len(written_files)},
        "errors": errors,
        "overall_status": "success" if val_status == "completed" else "failed_with_errors"
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
