import argparse
import sys
import uuid
from orchestrator.graph import build_orchestrator_graph
from orchestrator.state import ProjectState


def run_orchestrator(request: str, workspace_dir: str):
    run_id = str(uuid.uuid4())[:8]
    print(f"\n=======================================================")
    print(f"🚀 MULTI-AGENT ORCHESTRATOR [Run ID: {run_id}]")
    print(f"📁 Workspace: {workspace_dir}")
    print(f"📝 Request: {request}")
    print(f"=======================================================\n")

    app = build_orchestrator_graph()
    initial_state: ProjectState = {"request": request}

    for event in app.stream(initial_state):
        for node_name, node_output in event.items():
            print(f"--- [Node: {node_name.upper()}] ---")
            
            if node_name == "planner":
                print(f"Status: {node_output.get('planning_status')}")
                print(f"Architecture: {node_output.get('architecture', '')[:120]}...")
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
                    print(f"  • {art.get('filepath')}")

            elif node_name == "validation":
                print(f"Status: {node_output.get('validation_status')}")
                written = node_output.get("test_results", {}).get("written_files", [])
                print(f"Successfully saved {len(written)} file(s) into workspace:")
                for f in written:
                    print(f"  ✔ {f}")

            errors = node_output.get("errors", [])
            if errors:
                print(f"⚠️ Errors encountered: {errors}")
            print()

    print(f"=======================================================")
    print(f"🏁 Run ID {run_id} Finished.")
    print(f"=======================================================\n")


def main():
    parser = argparse.ArgumentParser(description="LangGraph Multi-Agent Software Orchestrator")
    parser.add_argument("prompt", help="Software project prompt or request")
    parser.add_argument("--workspace", default="./workspace_output", help="Target output workspace directory")
    args = parser.parse_args()

    run_orchestrator(args.prompt, args.workspace)


if __name__ == "__main__":
    main()
