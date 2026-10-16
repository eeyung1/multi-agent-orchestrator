import pytest
from langgraph.graph import END
from orchestrator.graph import route_after_planner


@pytest.mark.parametrize('state, expected', [
    ({'planning_status': 'failed'}, [END]),
    ({'frontend_tasks': ['UI'], 'backend_tasks': ['API']}, ['frontend', 'backend']),
    ({'backend_tasks': ['CLI']}, ['backend']),
    ({'frontend_tasks': ['UI']}, ['frontend']),
    ({}, ['validation']),
])
def test_routing(state, expected):
    assert route_after_planner(state) == expected
