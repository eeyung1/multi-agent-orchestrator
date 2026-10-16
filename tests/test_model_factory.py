import os
from types import SimpleNamespace
import pytest
from orchestrator import model_factory as factory


@pytest.fixture(autouse=True)
def clean_config(monkeypatch):
    for key in list(os.environ):
        if any(word in key for word in ('MODEL', 'API_KEY', 'TEMPERATURE', 'MAX_TOKENS')):
            monkeypatch.delenv(key)


@pytest.fixture
def imports(monkeypatch):
    calls = []
    def importer(module):
        calls.append(module)
        return SimpleNamespace(**{spec[1]: lambda **kwargs: kwargs for spec in factory.PROVIDERS.values()}, ChatCustom=lambda **kwargs: kwargs)
    monkeypatch.setattr(factory.importlib, 'import_module', importer)
    return calls


@pytest.mark.parametrize('provider', list(factory.PROVIDERS))
def test_providers_lazy_and_parameters(provider, monkeypatch, imports):
    monkeypatch.setenv('MODEL_PROVIDER', provider)
    monkeypatch.setenv('MODEL_NAME', 'new-model')
    monkeypatch.setenv('MAX_TOKENS', '700')
    monkeypatch.setenv('TEMPERATURE', '0.2')
    monkeypatch.setenv(f'{provider.upper()}_API_KEY', 'test-key')
    result = factory.get_chat_model('planner')
    assert imports == [factory.PROVIDERS[provider][0]]
    assert result['model'] == 'new-model'
    assert result['num_predict' if provider == 'ollama' else 'max_tokens'] == 700
    assert result['temperature'] == .2
    if provider != 'ollama':
        assert result['api_key'] == 'test-key'


def test_role_precedence(monkeypatch, imports):
    for key, value in {'MODEL_PROVIDER':'groq', 'MODEL_NAME':'global', 'PLANNER_MODEL_PROVIDER':'openai',
                       'PLANNER_MODEL_NAME':'role', 'MAX_TOKENS':'100', 'PLANNER_MAX_TOKENS':'200',
                       'TEMPERATURE':'1', 'PLANNER_TEMPERATURE':'0'}.items():
        monkeypatch.setenv(key, value)
    result = factory.get_chat_model('planner')
    assert result['model'] == 'role' and result['max_tokens'] == 200 and result['temperature'] == 0
    assert imports == ['langchain_openai']


def test_missing_optional_package(monkeypatch):
    monkeypatch.setenv('MODEL_PROVIDER', 'openai')
    def missing(module):
        raise ModuleNotFoundError(module)
    monkeypatch.setattr(factory.importlib, 'import_module', missing)
    with pytest.raises(ImportError, match='pip install langchain-openai'):
        factory.get_chat_model()


def test_new_compatible_provider(monkeypatch, imports):
    for key, value in {'MODEL_PROVIDER':'future', 'MODEL_NAME':'brand-new', 'MODEL_BASE_URL':'https://example.test/v1',
                       'FUTURE_API_KEY':'test'}.items():
        monkeypatch.setenv(key, value)
    result = factory.get_chat_model()
    assert result['api_key'] == 'test' and result['base_url'] == 'https://example.test/v1'
    assert imports == ['langchain_openai']


def test_custom_class_and_kwargs(monkeypatch, imports):
    monkeypatch.setenv('MODEL_CLASS', 'custom:ChatCustom')
    monkeypatch.setenv('MODEL_NAME', 'custom-model')
    monkeypatch.setenv('MODEL_KWARGS', '{"option": 1, "other": true}')
    monkeypatch.setenv('PLANNER_MODEL_KWARGS', '{"option": 2}')
    result = factory.get_chat_model('planner')
    assert result == {'model':'custom-model', 'option':2, 'other':True}


@pytest.mark.parametrize('key,value', [('MAX_TOKENS','0'), ('TEMPERATURE','nan'), ('MAX_TOKENS','1.5'), ('MODEL_KWARGS','[]')])
def test_invalid_settings(key, value, monkeypatch, imports):
    monkeypatch.setenv(key, value)
    with pytest.raises(ValueError):
        factory.get_chat_model()
