from orchestrator.graph import build_orchestrator_graph


def test_graph_compiles_with_nodes():
    app = build_orchestrator_graph()
    nodes = app.nodes
    assert "planner" in nodes
    assert "frontend" in nodes
    assert "backend" in nodes
