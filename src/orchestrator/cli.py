import argparse
import os
import sys
import uuid
from pathlib import Path
from orchestrator.graph import build_orchestrator_graph
from orchestrator.state import ProjectState


def find_blueprint(workspace_path: Path) -> str:
    candidates = ["blueprint.md", "README.md", "specs.md", "spec.md", "prompt.txt"]
    for name in candidates:
        candidate_file = workspace_path / name
        if candidate_file.is_file():
            print(f"📖 Found blueprint file: {candidate_file}")
            return candidate_file.read_text(encoding="utf-8")
    return ""


def run_orchestrator(prompt: str, workspace_dir: str, blueprint_file: str = None):
    run_id = str(uuid.uuid4())[:8]
    workspace_path = Path(workspace_dir).resolve()
    workspace_path.mkdir(
parents=True, exist_ok=True)

    request_content = prompt or ""
    if blueprint_file:
        bf_path = Path(blueprint_file).resolve()
        if bf_path.is_file():
            print(f"📖 Loading designated blueprint: {bf_path}")
            request_content = bf_path.read_text(encoding="utf-8")
    elif not request_content:
        request_content = find_blueprint(workspace_path)

    if not request_content.strip():
        print("❌ Error: No request provided and no blueprint (blueprint.md / specs.md / README.md) found in workspace.")
        sys.exit(1)

    print(f"\n======================================================")
    print(f"🚀 MULTI-AGENT ORCHESTRATOR [Run ID: {run_id}]")
    print(f"📁 Workspace: {workspace_path}")
    print(f"=====================================================\n")

    os.environ["TARGET_WORKSPACE_DIR"] = str(workspace_path)
    app = build_orchestrator_graph()
    initial_state: ProjectState = {"request": request_content}

    for event in app.stream(initial_state):
        for node_name, node_output in event.items():
            print(f"--- [Node: {node_name.upper()}] ---")

            if node_name == "planner":
                print(f"Status: {node_output.get('planning_status')}")
                print(f"Architecture: {node_output.get('architecture', '')[:140]}...")
                print(f"Frontend Tasks: {len(node_output.get('frontend_tasks', []))}")
                print(f"Backend Tasks:  {len(node_output.get('backend_tasks', []))}")

            elif node_name == "frontend":
                print(f"Status: {node_output.get('frontend_status')}")
                artifacts = node_output.get("frontend_artifacts", [])
                print(f"Generated {len(artifacts)} frontend file(s):")
                for art in artifacts:
                    print(f"  • {art.get('filepath')}")

            elif node_name == "backend":
                print(f"Status: {node_output.get('backend_status')}")
                artifacts = node_output.get("backend_artifacts", [])
                print(f"Generated {len(artifacts)} backend file(s):")
                for art in artifacts:
                    print(f"  •{art.get('filepath')}")

            elif node_name == "validation":
                print(f"Status: {node_output.get('validation_status')}")
                written = node_output.get('test_results', {}).get('written_files', [])
                print(f"Successfully written {len(written)} file(s) into workspace:")
                for f in written:
                    print(f"  ✅ {f}")

            errors = node_output.get('errors', [])
            if errors:
                print(f"≠ Errors: {errors}")
            print()

    print(f"====================================================")
    print(f"🏁 Run ID +{run_id} Completed Successfully.")
    print(f"====================================================\n")


def main():
    parser = argparse.ArgumentParser(description="LangGraph Multi-Agent Software Development Orchestrator")
    parser.add_argument("prompt", nargs="?", default="", help="Optional project prompt.")
    parser.add_argument("--workspace", required=True, help="Path to target workspace directory")
    parser.add_argument("--blueprint", default=None, help="Optional path to blueprint file")
    args = parser.parse_args()

    run_orchestrator(args.prompt, args.workspace, args.blueprint)


if __name__ == "__main__":
    main()
