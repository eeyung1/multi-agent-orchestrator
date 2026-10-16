"""Agent tests use deterministic chat-only models; never consume live API quotas."""
import pytest
from langchain_core.messages import AIMessage

PLAN = '''### ARCHITECTURE
Static HTML UI and a Python HTTP service.
### FRONTEND
index.html | Build user interface
### BACKEND
main.py | Build HTTP service
### END'''


class FakeModel:
    def __init__(self, role):
        self.role = role

    def invoke(self, messages):
        if self.role == 'planner':
            return AIMessage(content=PLAN)
        prompt = messages[-1].content
        import json
        task = json.loads(prompt.split('Current task: ')[-1])
        if isinstance(task, dict):
            path = task['filepath']
        elif 'db.py' in task:
            path = 'db.py'
        elif 'style.css' in task:
            path = 'style.css'
        else:
            path = 'index.html' if self.role == 'frontend' else 'main.py'
        return AIMessage(content=f'### FILE: {path}\n```text\nimplementation\n```')


@pytest.fixture(autouse=True)
def offline_agents(monkeypatch):
    for role in ('planner', 'frontend', 'backend'):
        monkeypatch.setattr(f'orchestrator.agents.{role}.get_chat_model', lambda role: FakeModel(role))
