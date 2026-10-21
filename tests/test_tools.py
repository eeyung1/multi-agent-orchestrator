import pytest
from orchestrator.tools.filesystem import SafeWorkspace


def test_safe_workspace_read_write(tmp_path):
    workspace = SafeWorkspace(str(tmp_path))
    
    # Write a file
    written = workspace.write_file("src/index.html", "<h1>Hello</h1>")
    assert written == "src/index.html"
    
    # Read the file
    content = workspace.read_file("src/index.html")
    assert content == "<h1>Hello</h1>"
    
    # List files
    files = workspace.list_files()
    assert "src/index.html" in files


def test_safe_workspace_boundary_enforcement(tmp_path):
    workspace = SafeWorkspace(str(tmp_path))
    
    # Attempt directory traversal out of workspace
    with pytest.raises((PermissionError, ValueError)):
        workspace.write_file("../escape.txt", "Malicious content")
        
    with pytest.raises((PermissionError, ValueError)):
        workspace.read_file("../../etc/passwd")


@pytest.mark.parametrize("git_marker", ["directory", "linked_worktree", "state_only"])
def test_legacy_generation_cannot_bypass_orchestrator(tmp_path, git_marker):
    root = tmp_path / "project"
    root.mkdir()
    if git_marker == "directory":
        (root / ".git").mkdir()
    elif git_marker == "linked_worktree":
        (root / ".git").write_text("gitdir: /some/shared/repository\n")
    else:
        state_dir = root / "multi-agent-orchestrator"
        state_dir.mkdir()
        (state_dir / "state.json").write_text("{}")
    workspace = SafeWorkspace(str(root / "nested"))
    with pytest.raises(PermissionError, match="scratch-only"):
        workspace.write_file("result.txt", "unclaimed work")
    assert not (root / "nested" / "result.txt").exists()


@pytest.mark.parametrize("path", [".git/config", "multi-agent-orchestrator/state.json",
                                  ".agent-worktrees/other/result.txt"])
def test_legacy_generation_cannot_create_authority_metadata(tmp_path, path):
    workspace = SafeWorkspace(str(tmp_path))
    with pytest.raises(PermissionError, match="metadata"):
        workspace.write_file(path, "forged")
