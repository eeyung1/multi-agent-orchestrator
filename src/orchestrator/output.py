"""Plain-text protocols with conservative fallbacks; never execute model output."""
import json
import re
from pathlib import PurePosixPath, PureWindowsPath


def safe_relative_path(value):
    path = str(value).strip().strip('`"\'')
    if (not path or PurePosixPath(path).is_absolute() or PureWindowsPath(path).drive
            or "\\" in path or ".." in PurePosixPath(path).parts or path == "."):
        raise ValueError(f"Expected a safe relative filepath, got {path!r}")
    return PurePosixPath(path).as_posix()


def unfence(text):
    text = text.strip()
    if text.startswith("```") or text.startswith("~~~"):
        lines = text.splitlines(keepends=True)
        marker = lines[0].strip()[:3]
        if len(lines) < 3 or lines[-1].strip() != marker:
            raise ValueError("Unclosed code fence; output may be truncated")
        return "".join(lines[1:-1])
    return text


def parse_plan(text):
    stripped = unfence(text)
    if stripped.startswith("{"):
        data = json.loads(stripped)
        architecture = data.get("architecture_overview") or data.get("architecture")
        frontend, backend = data.get("frontend_tasks", []), data.get("backend_tasks", [])
    else:
        sections = {}
        heading = None
        for line in stripped.splitlines():
            match = re.match(r"^\s*(?:#{1,3}\s*)?(ARCHITECTURE|FRONTEND|BACKEND|END)\s*:?\s*$", line, re.I)
            if match:
                heading = match[1].upper()
                sections.setdefault(heading, [])
            elif heading:
                sections[heading].append(line)
        if not {"ARCHITECTURE", "FRONTEND", "BACKEND", "END"}.issubset(sections):
            raise ValueError("Incomplete plan: expected ARCHITECTURE, FRONTEND, BACKEND and END sections")
        architecture = " ".join(sections["ARCHITECTURE"]).strip()
        frontend, backend = sections["FRONTEND"], sections["BACKEND"]
    if not isinstance(architecture, str) or not architecture.strip():
        raise ValueError("Plan has no architecture")

    def tasks(values):
        if not isinstance(values, list):
            raise ValueError("Plan tasks must be a list")
        result = []
        for value in values:
            if isinstance(value, dict):
                path, description = value.get("filepath", ""), value.get("description", "")
            elif isinstance(value, str):
                value = re.sub(r"^\s*(?:[-*]|\d+[.)])\s*", "", value).strip()
                if value.lower() in ("", "none", "(none)", "n/a"):
                    continue
                parts = re.split(r"\s*\|\s*|:\s+|\s+[-–]\s+", value, maxsplit=1)
                if len(parts) != 2:
                    raise ValueError(f"Task must be filepath | description: {value}")
                path, description = parts
            else:
                raise ValueError("Invalid task")
            if not isinstance(description, str) or not description.strip():
                raise ValueError("Task description is empty")
            result.append({"filepath": safe_relative_path(path), "description": description.strip()})
        return result

    frontend, backend = tasks(frontend), tasks(backend)
    paths = [task["filepath"] for task in frontend + backend]
    if not paths or len(paths) != len(set(paths)):
        raise ValueError("Plan must contain tasks with unique filepaths")
    return architecture.strip(), frontend, backend


def parse_file(text, expected_path=None):
    expected_path = safe_relative_path(expected_path) if expected_path else None
    # Legacy JSON is accepted as plain text, without function/tool calling.
    stripped = unfence(text) if text.startswith(("```json", "~~~json")) else text.strip()
    try:
        data = json.loads(stripped)
    except ValueError:
        data = None
    if isinstance(data, dict) and "filepath" in data and "content" in data:
        path, content = safe_relative_path(data["filepath"]), data["content"]
    else:
        matches = list(re.finditer(r"^\s*#{1,3}\s*FILE:\s*(.+?)\s*$", stripped, re.M | re.I))
        if len(matches) > 1:
            raise ValueError("Expected exactly one file per task")
        if matches:
            path = safe_relative_path(matches[0][1])
            content = unfence(stripped[matches[0].end():])
        elif expected_path:
            path, content = expected_path, unfence(stripped)
        else:
            raise ValueError("Missing ### FILE: <path> delimiter")
    if expected_path and path != expected_path:
        raise ValueError(f"Model returned {path!r}; expected {expected_path!r}")
    if not isinstance(content, str) or not content.strip():
        raise ValueError("Empty file content")
    return {"filepath": path, "content": content, "description": "Generated implementation"}
