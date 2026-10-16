import os
import sys
import uuid
import argparse
from orchestrator.graph import build_orchestrator_graph

def find_blueprint_content(workspace: str, explicit_blueprint: str = None) -> str:
    if explicit_blueprint:
        with open(explicit_blueprint, "r", encoding="utf-8") as f:
            return f.read().strip()

    candidates = ["blueprint.md", "README.md", "specs.md"]
    for c in candidates:
        full_path = os.path.join(workspace, c)
        if os.path.isfile(full_path):
            with open(full_path, "r", encoding="utf-8") as f:
                content = f.read().strip()
                if content:
                    print(f"📖 Found blueprint file: {full_path}")
                    return content
    return ""

def run_orchestrator(prompt: str, workspace: str, blueprint: str = None):
    run_id = uuid.uuid4().hex[:8]
    abs_workspace = os.path.abspath(workspace)

    effective_prompt = prompt or find_blueprint_content(abs_workspace, blueprint)
    if not effective_prompt:
        print("❌ Error: No prompt provided and no valid blueprint found in workspace.")
        sys.exit(1)

    print("\n======================================================")
    print(f"🚀 MULTI-AGENT ORCHESTRATOR [Run ID: {run_id}]")
    print(f"📁 Workspace: {abs_workspace}")
    print("=====================================================\n")

    initial_state = {
        "project_prompt": effective_prompt,
        "request": effective_prompt,
        "target_workspace": abs_workspace,
        "architecture_overview": None,
        "frontend_tasks": [],
        "backend_tasks": [],
        "planning_status": None,
        "frontend_artifacts": [],
        "frontend_status": None,
        "backend_artifacts": [],
        "backend_status": None,
        "validation_status": None,
        "test_results": {},
        "errors": [],
        "overall_status": None,
    }

    app = build_orchestrator_graph()

    overall_status = None
    for event in app.stream(initial_state):
        for node_name, node_output in event.items():
            overall_status = node_output.get("overall_status", overall_status)
            print(f"--- [Node: {node_name.upper()}] ---")
            if "planning_status" in node_output:
                print(f"Status: {node_output.get('planning_status')}")
                arch = node_output.get("architecture_overview") or ""
                print(f"Architecture: {arch[:120]}..." if len(arch) > 120 else f"Architecture: {arch}")
                print(f"Frontend Tasks: {len(node_output.get('frontend_tasks') or [])}")
                print(f"Backend Tasks:  {len(node_output.get('backend_tasks') or [])}")
            if "frontend_status" in node_output:
                print(f"Status: {node_output.get('frontend_status')}")
                print(f"Generated {len(node_output.get('frontend_artifacts') or [])} frontend file(s)")
            if "backend_status" in node_output:
                print(f"Status: {node_output.get('backend_status')}")
                print(f"Generated {len(node_output.get('backend_artifacts') or [])} backend file(s)")
            if "validation_status" in node_output:
                print(f"Validation: {node_output.get('validation_status')}")
                print(f"Overall Status: {node_output.get('overall_status')}")
            if node_output.get("errors"):
                print(f"≠ Errors: {node_output.get('errors')}")
            print()

    print("====================================================")
    print(f"🏁 Run ID {run_id}: {overall_status or 'failed'}")
    print("====================================================\n")
    return 0 if overall_status == "success" else 1

def main():
    parser = argparse.ArgumentParser(description="Multi-Agent Development Orchestrator")
    parser.add_argument("--prompt", type=str, default="", help="Direct instruction for code generation")
    parser.add_argument("--workspace", type=str, default=".", help="Target workspace path")
    parser.add_argument("--blueprint", type=str, default=None, help="Explicit path to blueprint markdown file")
    args = parser.parse_args()

    try:
        return run_orchestrator(args.prompt, args.workspace, args.blueprint)
    except Exception as exc:
        print(f"Orchestration failed: {exc}", file=sys.stderr)
        return 1

if __name__ == "__main__":
    sys.exit(main())
