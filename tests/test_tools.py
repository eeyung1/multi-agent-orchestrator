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
