from orchestrator.state import ProjectState


def test_project_state_initialization():
    initial_state: ProjectState = {
        "request": "Build a markdown viewer",
        "planning_status": "pending",
        "overall_status": "initialized",
    }
    assert initial_state["request"] == "Build a markdown viewer"
    assert initial_state["planning_status"] == "pending"
    assert "architecture" not in initial_state


def test_project_state_updates():
    state: ProjectState = {
        "request": "Build a REST API",
        "frontend_tasks": [],
        "backend_tasks": ["Create DB schema"],
        "errors": [],
    }
    state["backend_status"] = "in_progress"
    assert state["backend_status"] == "in_progress"
    assert len(state["backend_tasks"]) == 1
