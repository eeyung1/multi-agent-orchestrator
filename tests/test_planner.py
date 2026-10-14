import pytest
from orchestrator.agents.planner import planning_node
from orchestrator.state import ProjectState


def test_planner_empty_request():
    state: ProjectState = {"request": ""}
    result = planning_node(state)
    assert result["planning_status"] == "failed"
    assert len(result["errors"]) > 0


def test_planner_generates_valid_plan():
    state: ProjectState = {
        "request": "Build a simple markdown previewer web application with an input area and rendered output."
    }
    result = planning_node(state)
    assert result["planning_status"] == "completed"
    assert len(result["clarified_requirements"]) > 0
    assert isinstance(result["architecture"], str) and len(result["architecture"]) > 0
    assert len(result["frontend_tasks"]) > 0
    assert len(result["backend_tasks"]) > 0
    assert result["overall_status"] == "planned"
