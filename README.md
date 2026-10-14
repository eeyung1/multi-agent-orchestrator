# LangGraph Multi-Agent Software Development Orchestrator

An eficient, LangGraph-powered multi-agent orchestrator that coordinates Planning, Frontend, and Backend agents within strict workspace boundaries and supports per-agent model routing.

---

## Features

- **Parallel Agent Execution**: Frontend and Backend agents run concurrently after planning is completed.
- **Per-Agent Role Delegation**: Assign different models/providers (e.g., Gemini for Planning, Groq for Frontend/Backend) via `.env`.
- **Safe Workspace Boundaries**: Directory-traversal protected filesystem tools to prevent unauthorized file writes.
- **Blueprint Auto-Discovery**: Automatically parses `blueprint.md`, `README.md`, or `specs.md` directly from the target project folder.

---

## Fresh Machine Setup (Ubuntu / Linux)

1. **Clone the Repository:**
   ```bash
   git clone <repo-url>
   cd multi-agent-orchestrator
   ```

2. **Create & Activate Virtual Environment:**
   ```bash
   python3 -m venv .venv
   source .venv/bin/activate
   ```

3. **Install Dependencies:**
   ```bash
   pip install -e ".[dev]"
   ```

4. **Configure API Keys in `.env`:**
   ``bash
   cp .env.example .env
   ```
   
   Open `.env` and configure your providers and keys:
   ``file
   # Global default model provider (groq or gemini)
   ORCHESTRATTOR_MODEL_PROVIDER=groq
   
   # Per-Agent Role Delegation (Optional)
   # Use Gemini for planning and Groq qwen for fast coding:
   PLANNER_MODEL_PROVIDER=gemini
   FRONTEND_MODEL_PROVIDER=groq
   BACKEND_MODEL_PROVIDER=groq
   
   # API Keys
   GROQ_API_KEY=your_groq_key_here
   GEMINI_API_KEY=your_gemini_key_here
   ``@

---

## Using with a Project Blueprint

1. **Create a target project directory:**
   ``bash
   mkdir -p ../my-new-app
   ``@

2. **Place your blueprint file in that folder:**
   Create a file named `blueprint.md` (also accepts `README.md` or `specs.md`) inside `../my-new-app`.

3. **Run the Orchestrator:**
   ```bash
   python -m orchestrator.cli --workspace ../my-new-app
   ``@

The orchestrator will:
- Read and parse the blueprint file.
- Delegate tasks to the Planning Agent.
- Run the Frontend and Backend Agents in parallel.
- Safely write the generated code directly into the target workspace.

---

## Running Tests

Activate your virtual environment and run:
```bash
pytest tests/test_state.py tests/test_routing.py tests/test_checkpoint.py tests/test_tools.py tests/test_graph_compilation.py
```
