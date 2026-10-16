import os
from orchestrator.graph import build_orchestrator_graph
from orchestrator.state import ProjectState


def test_end_to_end_orchestration(tmp_path, monkeypatch):
    # Set the safe workspace directory to pytest's isolated tmp_path
    monkeypatch.setenv("TARGET_WORKSPACE_DIR", str(tmp_path))

    app = build_orchestrator_graph()

    initial_state: ProjectState = {
        "request": "Build a simple single-page stopwatch with start, pause, and reset buttons."
    }

    final_state = app.invoke(initial_state)

    # 1. Verify planning completed
    assert final_state["planning_status"] == "completed"
    assert final_state["architecture_overview"]

    # 2. Verify frontend completed
    assert final_state["frontend_status"] == "completed"
    assert len(final_state.get("frontend_artifacts", [])) > 0

    # 3. Verify validation completed and files exist inside authorized workspace
    assert final_state["validation_status"] == "completed"
    assert final_state["overall_status"] == "success"

    written = final_state["test_results"]["written_files"]
    assert len(written) > 0

    for rel_path in written:
        file_path = tmp_path / rel_path
        assert file_path.exists()
        assert len(file_path.read_text(encoding="utf-8")) > 0
