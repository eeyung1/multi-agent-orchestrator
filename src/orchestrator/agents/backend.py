"""Provider-agnostic backend agent."""
from orchestrator.state import ProjectState
from orchestrator.model_factory import get_chat_model
from orchestrator.agents.worker import implement_files


def backend_node(state: ProjectState) -> dict:
    return implement_files(state, "backend", get_chat_model)
