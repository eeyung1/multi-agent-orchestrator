from langgraph.checkpoint.memory import MemorySaver
from langgraph.graph import StateGraph, START, END
from orchestrator.state import ProjectState


def dummy_planner(state: ProjectState) -> dict:
    return {
        "planning_status": "completed",
        "frontend_tasks": ["Build UI"],
        "backend_tasks": ["Build API"]
    }


def dummy_frontend(state: ProjectState) -> dict:
    return {
        "frontend_status": "completed"
    }


def test_graph_state_checkpoint_and_resume():
    checkpointer = MemorySaver()
    builder = StateGraph(ProjectState)

    builder.add_node("planner", dummy_planner)
    builder.add_node("frontend", dummy_frontend)

    builder.add_edge(START, "planner")
    builder.add_edge("planner", "frontend")
    builder.add_edge("frontend", END)

    app = builder.compile(checkpointer=checkpointer)

    thread_config = {"configurable": {"thread_id": "session-101"}}

    # Execute workflow under a thread ID
    initial_input: ProjectState = {"request": "Create a task tracker"}
    result = app.invoke(initial_input, config=thread_config)

    assert result["planning_status"] == "completed"
    assert result["frontend_status"] == "completed"

    # Inspect checkpoint persistence
    checkpoint_state = app.get_state(thread_config)
    assert checkpoint_state.values["request"] == "Create a task tracker"
    assert checkpoint_state.values["frontend_status"] == "completed"
