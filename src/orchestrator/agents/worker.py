"""Shared single-file implementation workflow. Validation owns all disk writes."""
import json
from langchain_core.messages import HumanMessage, SystemMessage
from orchestrator.llm import invoke_text
from orchestrator.output import parse_file


def implement_files(state, role, model_factory):
    tasks = state.get(f"{role}_tasks") or []
    if not tasks:
        return {f"{role}_status": "skipped", f"{role}_artifacts": []}
    artifacts, errors = [], []
    try:
        model = model_factory(role=role)
    except Exception as exc:
        return {f"{role}_status": "failed", f"{role}_artifacts": [],
                "errors": [f"{role.capitalize()} model configuration error: {exc}"]}
    system = (
        f"You are a {role} engineer. Implement exactly ONE requested file completely. "
        "Return only ### FILE: <relative filepath> on its own line, followed by "
        "a fenced code block containing the complete file. Close the fence. "
        "No tools, explanations, placeholders or TODOs. Keep code concise. "
        "Preserve the requested path and use the shared architecture/interfaces."
    )
    for task in tasks:
        try:
            expected = task.get("filepath") if isinstance(task, dict) else None
            prompt = (f"Project: {state.get('request') or state.get('project_prompt') or ''}\n"
                      f"Architecture: {state.get('architecture_overview') or ''}\n"
                      f"All frontend tasks: {json.dumps(state.get('frontend_tasks') or [])}\n"
                      f"All backend tasks: {json.dumps(state.get('backend_tasks') or [])}\n"
                      f"Current task: {json.dumps(task)}")
            text = invoke_text(model, [SystemMessage(content=system), HumanMessage(content=prompt)], role)
            artifact = parse_file(text, expected)
            if any(item["filepath"] == artifact["filepath"] for item in artifacts):
                raise ValueError(f"Duplicate filepath: {artifact['filepath']}")
            artifacts.append(artifact)
        except Exception as exc:
            errors.append(f"{role.capitalize()} task {task!r} failed: {exc}")
    return {f"{role}_artifacts": artifacts,
            f"{role}_status": "failed" if errors else "completed", "errors": errors}
