import operator
from typing import List, Dict, Any, Optional, Annotated
from typing_extensions import TypedDict

class ProjectState(TypedDict):
    request: str
    project_prompt: Optional[str]
    target_workspace: str
    architecture_overview: Optional[str]
    frontend_tasks: Optional[List[Dict[str, Any]]]
    backend_tasks: Optional[List[Dict[str, Any]]]
    planning_status: Optional[str]
    
    # Reducer channels safe for parallel branch writes
    frontend_artifacts: Annotated[List[Dict[str, Any]], operator.add]
    frontend_status: Optional[str]
    
    backend_artifacts: Annotated[List[Dict[str, Any]], operator.add]
    backend_status: Optional[str]
    
    validation_status: Optional[str]
    test_results: Optional[Dict[str, Any]]
    errors: Annotated[List[str], operator.add]
    overall_status: Optional[str]
