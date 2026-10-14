# LangGraph Multi-Agent Software Development Orchestrator

A system built with LangGraph coordinating Planning, Frontend, and Backend agents.

---

## 1. Environment Activation

Always activate the virtual environment:

```bash
cd /home/student/multi-agent-orchestrator
source .venv/bin/activate
```

---

## 2. Running Against a Project Blueprint Folder

Place your blueprint `blueprint.md` in your target workspace directory:

```bash
mkdir -p ./sample_project
cp your_blueprint.md ./sample_project/blueprint.md
```

Run the orchestrator pointing to that folder:

```bash
python -m orchestrator.cli --workspace ./sample_project
```

The orchestrator will:
1. Read blueprint.md from the workspace.
2. Plan the architecture and components.
3. Implement frontend and backend artifacts.
4. Safely write files directly into the target workspace.

---

## 3. Running Tests

```bash
pytest tests/test_state.py tests/test_routing.py tests/test_checkpoint.py tests/test_tools.py tests/test_graph_compilation.py
```
