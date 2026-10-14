import pytest
from orchestrator.agents.frontend import frontend_node
from orchestrator.state import ProjectState


def test_frontend_skipped_when_no_tasks():
    state: ProjectState = {
        "request": "A CLI tool",
        "frontend_tasks": []
    }
    result = frontend_node(state)
    assert result["frontend_status"] == "skipped"
    assert result["frontend_artifacts"] == []


def test_frontend_generates_code_artifacts():
    state: ProjectState = {
        "request": "Markdown previewer app",
        "architecture": "Single Page HTML/JS interface with CSS styling",
        "frontend_tasks": [
            "Create index.html with textarea for input and div for preview",
            "Add basic styling in style.css"
        ]
    }
    result = frontend_node(state)
    assert result["frontend_status"] == "completed"
    assert len(result["frontend_artifacts"]) > 0
    assert "filepath" in result["frontend_artifacts"][0]
    assert len(result["frontend_artifacts"][0]["content"]) > 0
