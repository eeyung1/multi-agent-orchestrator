# LangGraph Multi-Agent Software Development Orchestrator

A LangGraph-powered multi-agent orchestrator that coordinates Planning, Frontend, and Backend agents within strict workspace boundaries.

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
   Open `.env` and set your preferred provider and keys:
   ``ini
   # Set provider to groq or gemini
   ORCHESTRATTOR_MODEL_PROVIDER=groq
   
   # For Groq (free tier on console.groq.com)
   GROQ_API_KEY=your_groq_key_here

   # For Gemini (free tier on aistudio.google.com)
   GEMINI_API_KEY=your_gemini_key_here
   ``@

---

## How to Use With a Project Blueprint

1. **Create a target project directory:**
   ```bash
   mkdir -p ../my-new-app
   ``@

2. **Place your blueprint file in that folder:**
   Create a file named `blueprint.md` (also accepts `README.md` or `specs.md`) inside `../my-new-app`.

3. **Run the Orchestrator:**
   ``bash
   python -m orchestrator.cli --workspace ../my-new-app
   ``@

The orchestrator will:
- Automatically detect and read `blueprint.md`.
- Coordinate the Planning, Frontend, and Backend agents.
- Safely write the complete source files directly into the workspace.

---

## Running Unit & Routing Tests

```bash
pytest tests/test_state.py tests/test_routing.py tests/test_checkpoint.py tests/test_tools.py tests/test_graph_compilation.py
```
