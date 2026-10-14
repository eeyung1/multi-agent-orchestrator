from typing import TypedDict, List, Optional, Dict, Any


class ProjectState(TypedDict, total=False):
    # Requirements & Planning
    request: str
    clarified_requirements: List[str]
    architecture: str
    proposed_file_structure: List[str]
    planning_status: str

    # Frontend Track
    frontend_tasks: List[str]
    frontend_status: str
    frontend_artifacts: List[Dict[str, Any]]

    # Backend Track
    backend_tasks: List[str]
    backend_status: str
    backend_artifacts: List[Dict[str, Any]]

    # Validation & Quality
    validation_status: str
    test_results: Dict[str, Any]
    errors: List[str]

    # Human-in-the-loop & Orchestrator Control
    pending_human_approval: Optional[str]
    overall_status: str
