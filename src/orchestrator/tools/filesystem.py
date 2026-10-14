import os
from pathlib import Path
from typing import List, Optional


class SafeWorkspace:
    def __init__(self, workspace_root: str):
        self.root = Path(workspace_root).resolve()
        self.root.mkdir(parents=True, exist_ok=True)

    def _resolve_safe_path(self, relative_path: str) -> Path:
        target = (self.root / relative_path).resolve()
        # Verify the target path is strictly within the workspace root
        if not str(target).startswith(str(self.root)):
            raise PermissionError(f"Access denied: path '{relative_path}' traverses outside workspace boundary.")
        return target

    def write_file(self, relative_path: str, content: str) -> str:
        target = self._resolve_safe_path(relative_path)
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
