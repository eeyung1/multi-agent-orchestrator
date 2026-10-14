from langgraph.graph import END
from orchestrator.graph import route_after_planner, route_after_frontend
from orchestrator.state import ProjectState


def test_route_planner_failure():
    state: ProjectState = {"planning_status": "failed"}
    assert route_after_planner(state) == END


def test_route_planner_to_frontend():
    state: ProjectState = {
        "planning_status": "completed",
        "frontend_tasks": ["Build UI"],
        "backend_tasks": ["Build API"]
    }
    assert route_after_planner(state) == "frontend"


def test_route_planner_bypasses_to_backend():
    state: ProjectState = {
        "planning_status": "completed",
        "frontend_tasks": [],
        "backend_tasks": ["Build CLI script"]
    }
    assert route_after_planner(state) == "backend"


def test_route_frontend_to_backend():
    state: ProjectState = {
        "frontend_status": "completed",
        "backend_tasks": ["Build DB"]
    }
    assert route_after_frontend(state) == "backend"


def test_route_frontend_to_validation_when_no_backend():
    state: ProjectState = {
        "frontend_status": "completed",
        "backend_tasks": []
    }
    assert route_after_frontend(state) == "validation"
