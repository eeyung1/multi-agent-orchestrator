import os
from pathlib import Path
from orchestrator.output import safe_relative_path
from typing import List, Optional


class SafeWorkspace:
    def __init__(self, workspace_root: str):
        self.root = Path(workspace_root).resolve()
        self.root.mkdir(parents=True, exist_ok=True)

    def _resolve_safe_path(self, relative_path: str) -> Path:
        target = (self.root / safe_relative_path(relative_path)).resolve()
        # Verify the target path is strictly within the workspace root
        if not target.is_relative_to(self.root):
            raise PermissionError(f"Access denied: path '{relative_path}' traverses outside workspace boundary.")
        return target

    def write_file(self, relative_path: str, content: str) -> str:
        target = self._resolve_safe_path(relative_path)
        # The legacy graph may generate scratch artifacts, but it must never be
        # a second execution/ownership path in a managed Git repository. Reading
        # project context or using planning_node for proposals remains possible.
        if any(part in {".git", ".agent-worktrees", "multi-agent-orchestrator"}
               for part in Path(relative_path).parts):
            raise PermissionError("Legacy generation cannot write coordination or Git metadata.")
        for parent in (target.parent, *target.parent.parents):
            if ((parent / ".git").exists()
                    or (parent / "multi-agent-orchestrator" / "state.json").exists()):
                raise PermissionError(
                    "Legacy generation is scratch-only; use the Go Planner/ORCHESTRATOR "
                    "for Git repositories and managed worktrees."
                )
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")
        return str(target.relative_to(self.root))

    def read_file(self, relative_path: str) -> str:
        target = self._resolve_safe_path(relative_path)
        if not target.exists():
            raise FileNotFoundError(f"File '{relative_path}' not found in workspace.")
        return target.read_text(encoding="utf-8")

    def list_files(self) -> List[str]:
        results = []
        for path in self.root.rglob("*"):
            if path.is_file():
                results.append(str(path.relative_to(self.root)))
        return sorted(results)
