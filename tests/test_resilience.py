from unittest.mock import Mock
from types import SimpleNamespace
import pytest
from langchain_core.messages import AIMessage
from orchestrator.llm import invoke_text
from orchestrator.output import parse_file, parse_plan
from orchestrator.graph import build_orchestrator_graph, validation_node
from orchestrator.cli import run_orchestrator, main
from orchestrator.tools.filesystem import SafeWorkspace
from conftest import PLAN


@pytest.mark.parametrize('message', ['HTTP 429', 'RESOURCE_EXHAUSTED', 'rate_limit_exceeded', 'tokens per minute exceeded'])
def test_backoff_and_success(message, monkeypatch):
    sleep = Mock()
    monkeypatch.setattr('orchestrator.llm.time.sleep', sleep)
    monkeypatch.setattr('orchestrator.llm.random.uniform', lambda a, b: 0)
    monkeypatch.setenv('LLM_RETRY_BASE_SECONDS', '2')
    model = Mock()
    model.invoke.side_effect = [RuntimeError(message), RuntimeError(message), AIMessage(content='ok')]
    assert invoke_text(model, [], 'backend') == 'ok'
    assert [call.args[0] for call in sleep.call_args_list] == [2, 4]


def test_exhaustion_and_nonretryable(monkeypatch):
    monkeypatch.setattr('orchestrator.llm.time.sleep', lambda _: None)
    monkeypatch.setenv('LLM_MAX_ATTEMPTS', '3')
    model = Mock()
    model.invoke.side_effect = RuntimeError('429')
    with pytest.raises(RuntimeError):
        invoke_text(model, [], 'planner')
    assert model.invoke.call_count == 3
    model.reset_mock()
    model.invoke.side_effect = RuntimeError('401 Unauthorized')
    with pytest.raises(RuntimeError):
        invoke_text(model, [], 'planner')
    assert model.invoke.call_count == 1


def test_retry_after(monkeypatch):
    sleep = Mock()
    monkeypatch.setattr('orchestrator.llm.time.sleep', sleep)
    error = RuntimeError('rate limit')
    error.response = SimpleNamespace(status_code=429, headers={'retry-after':'15'})
    model = Mock()
    model.invoke.side_effect = [error, AIMessage(content='ok')]
    invoke_text(model, [], 'planner')
    assert sleep.call_args.args[0] == 15


def test_text_blocks_and_reasoning():
    model = Mock()
    model.invoke.return_value = AIMessage(content=[{'type':'text', 'text':'<think>reason</think>answer'}])
    assert invoke_text(model, [], 'planner') == 'answer'


def test_truncation():
    model = Mock()
    model.invoke.return_value = AIMessage(content='partial', response_metadata={'finish_reason':'length'})
    with pytest.raises(ValueError, match='truncated'):
        invoke_text(model, [], 'planner')


@pytest.mark.parametrize('text', [
    '### FILE: app.py\n```python\nprint(1)\n```',
    '```python\nprint(1)\n```', 'print(1)',
    '{"filepath":"app.py","content":"print(1)"}',
])
def test_file_formats(text):
    artifact = parse_file(text, 'app.py')
    assert artifact['filepath'] == 'app.py'
    assert artifact['content'].strip() == 'print(1)'


@pytest.mark.parametrize('text', ['### FILE: ../escape.py\n```\nx\n```',
    '### FILE: wrong.py\n```\nx\n```', '```python\npartial',
    '### FILE: app.py\n```\nx\n```\n### FILE: extra.py\n```\ny\n```'])
def test_invalid_files(text):
    with pytest.raises(ValueError):
        parse_file(text, 'app.py')


def test_plan_fallback_and_truncation():
    assert parse_plan(PLAN)[1][0]['filepath'] == 'index.html'
    assert parse_plan('{"architecture":"API", "backend_tasks":[{"filepath":"main.py", "description":"serve"}]}')[2]
    with pytest.raises(ValueError, match='Incomplete'):
        parse_plan(PLAN.replace('### END', ''))
    with pytest.raises(ValueError, match='unique'):
        parse_plan(PLAN.replace('main.py', 'index.html'))


def test_parallel_errors_not_duplicated(tmp_path, monkeypatch):
    monkeypatch.setattr('orchestrator.agents.frontend.get_chat_model', Mock(side_effect=ValueError('bad frontend config')))
    monkeypatch.setattr('orchestrator.agents.backend.get_chat_model', Mock(side_effect=ValueError('bad backend config')))
    result = build_orchestrator_graph().invoke({'request':'app', 'target_workspace':str(tmp_path), 'errors':['previous']})
    assert len(result['errors']) == 3
    assert result['errors'].count('previous') == 1
    assert result['overall_status'] == 'failed_with_errors'


def test_partial_failure_is_failed(monkeypatch):
    from orchestrator.agents.backend import backend_node
    monkeypatch.setenv('LLM_MAX_ATTEMPTS', '1')
    model = Mock()
    model.invoke.side_effect = [AIMessage(content='print(1)'), RuntimeError('429')]
    monkeypatch.setattr('orchestrator.agents.backend.get_chat_model', lambda **_: model)
    result = backend_node({'backend_tasks':[{'filepath':'a.py'}, {'filepath':'b.py'}]})
    assert result['backend_status'] == 'failed'
    assert len(result['backend_artifacts']) == len(result['errors']) == 1


def test_workspace_priority_and_no_state_mutation(tmp_path, monkeypatch):
    monkeypatch.setenv('TARGET_WORKSPACE_DIR', str(tmp_path / 'wrong'))
    state = {'target_workspace': str(tmp_path / 'right'), 'errors':['existing'],
             'frontend_artifacts':[{'filepath':'app.py','content':'hello'}]}
    result = validation_node(state)
    assert (tmp_path / 'right/app.py').read_text() == 'hello'
    assert not (tmp_path / 'wrong').exists()
    assert result['errors'] == [] and state['errors'] == ['existing']
    assert result['overall_status'] == 'failed_with_errors'


def test_workspace_sibling_and_symlink_escape(tmp_path):
    root = tmp_path / 'workspace'
    sibling = tmp_path / 'workspace-other'
    sibling.mkdir()
    workspace = SafeWorkspace(str(root))
    (root / 'link').symlink_to(sibling, target_is_directory=True)
    for path in ('../workspace-other/file', 'link/file', str(sibling / 'file')):
        with pytest.raises((ValueError, PermissionError)):
            workspace.write_file(path, 'bad')
    assert not (sibling / 'file').exists()


@pytest.mark.parametrize('tasks', [('frontend',), ('backend',), ('frontend','backend')])
def test_fanin_runs_validation_once(tmp_path, monkeypatch, tasks):
    def planner(_):
        return {'planning_status':'completed', **{f'{role}_tasks':[{'filepath':f'{role}.txt', 'description':'write'}] for role in tasks}}
    monkeypatch.setattr('orchestrator.graph.planning_node', planner)
    events = list(build_orchestrator_graph().stream({'request':'app', 'target_workspace':str(tmp_path)}))
    assert sum('validation' in event for event in events) == 1
    assert len(list(tmp_path.iterdir())) == len(tasks)


def test_cli_blueprint_and_failure_exit(tmp_path, monkeypatch, capsys):
    (tmp_path / 'blueprint.md').write_text('Build an app')
    assert run_orchestrator('', str(tmp_path)) == 0
    assert (tmp_path / 'index.html').exists()
    monkeypatch.setattr('orchestrator.agents.planner.get_chat_model', Mock(side_effect=ValueError('configuration failed')))
    monkeypatch.setattr('sys.argv', ['orchestrator', '--workspace', str(tmp_path)])
    assert main() == 1
    assert 'Completed Successfully' not in capsys.readouterr().out


def test_normalized_duplicate_paths():
    with pytest.raises(ValueError, match='unique'):
        parse_plan(PLAN.replace('main.py', './index.html'))


def test_explicit_missing_blueprint_fails(tmp_path, monkeypatch):
    (tmp_path / 'README.md').write_text('must not silently use this')
    monkeypatch.setattr('sys.argv', ['orchestrator', '--workspace', str(tmp_path), '--blueprint', str(tmp_path / 'missing.md')])
    assert main() == 1


def test_module_entrypoint_subprocess(tmp_path):
    """Run the real -m entrypoint with only provider construction replaced offline."""
    import os
    import subprocess
    import sys
    from pathlib import Path
    shim = tmp_path / 'shim'
    shim.mkdir()
    # sitecustomize is loaded before -m. No production mock/test switch is added.
    (shim / 'sitecustomize.py').write_text('''
from langchain_core.messages import AIMessage
from orchestrator import model_factory
class Model:
    def __init__(self, role): self.role = role
    def invoke(self, messages):
        if self.role == 'planner':
            return AIMessage(content='### ARCHITECTURE\\nStatic page.\\n### FRONTEND\\nindex.html | Build page\\n### BACKEND\\nnone\\n### END')
        return AIMessage(content='### FILE: index.html\\n```html\\n<h1>Done</h1>\\n```')
model_factory.get_chat_model = lambda role: Model(role)
''')
    workspace = tmp_path / 'project'
    workspace.mkdir()
    (workspace / 'blueprint.md').write_text('Build a page')
    env = os.environ.copy()
    env['PYTHONPATH'] = os.pathsep.join([str(shim), str(Path(__file__).resolve().parents[1] / 'src')])
    result = subprocess.run([sys.executable, '-m', 'orchestrator.cli', '--workspace', str(workspace)],
                            capture_output=True, text=True, env=env, timeout=30)
    assert result.returncode == 0, result.stdout + result.stderr
    assert (workspace / 'index.html').read_text().strip() == '<h1>Done</h1>'
    assert ': success' in result.stdout
