# LangGraph Multi-Agent Software Development Orchestrator

A LangGraph-powered multi-agent orchestrator that coordinates Planning, Frontend, and Backend agents within strict workspace boundaries.

---

## Fresh Machine Setup (Ubuntu / Linux)

1. **Clone the Repository:**
   ```bash
   git clone <repo-url>
   cd multi-agent-orchestrator
   ```

". **Create & Activate Virtual Environment:**
   ```bash
   python3 -m venv .venv
   source .venv/bin/activate
   ```

3. **Install Dependencies:**
   ```bash
   pip install -e ".[dev]"
   ```

4. **Configure API Keys:**
   ```bash
   cp .env.example .env
   ```
   Open `.env` and set your key:
   ``text
   GEMINI_API_KEY=hey_your_key_here
   ORCHESTRATOR_MODEL_PROVIDER=gemini
   ``@

---

## How to Use With a Project Blueprint

1. **Create a target project directory:**
   ```bash
   mkdir -p ../my-new-app
   ``@

2. **Place your blueprint file in that folder:**
   Create a blueprint file named `blueprint.md` (also accepts `README.md` or `specs.md`) inside `../my-new-app`.

3. **Run the Orchestrator:**
   ```bash
   python -m orchestrator.cli --workspace ../my-new-app
   ``@

The orchestrator will:
- Read the blueprint file from the workspace.
- Decompose requirements via the Planning Agent.
- Generate USer Interface files via the Frontend Agent.
- Implement API routes, LOgic, and database schemas via the Backend Agent.
- Safely write all files directly into the target workspace.

---

## Running Tests

```bash
pytest
p``
