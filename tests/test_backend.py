import pytest
from orchestrator.agents.backend import backend_node
from orchestrator.state import ProjectState


def test_backend_skipped_when_no_tasks():
    state: ProjectState = {
        "request": "Pure static HTML site",
        "backend_tasks": []
    }
    result = backend_node(state)
    assert result["backend_status"] == "skipped"
    assert result["backend_artifacts"] == []


def test_backend_generates_code_artifacts():
    state: ProjectState = {
        "request": "Note taking app",
        "architecture_overview": "FastAPI service with SQLite database",
        "backend_tasks": [
            "Create SQLite database initialization script in db.py",
            "Implement POST /notes endpoint in main.py"
        ]
    }
    result = backend_node(state)
    assert result["backend_status"] == "completed"
    assert len(result["backend_artifacts"]) > 0
    assert "filepath" in result["backend_artifacts"][0]
    assert len(result["backend_artifacts"][0]["content"]) > 0
