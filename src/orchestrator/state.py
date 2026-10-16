"""Shared graph channels. Nodes return only NEW values for additive channels."""
import operator
from typing import Annotated, Any, TypedDict


class FileTask(TypedDict):
    filepath: str
    description: str


class FileArtifact(FileTask):
    content: str


class ProjectState(TypedDict, total=False):
    request: str
    project_prompt: str | None  # Backward-compatible input alias.
    target_workspace: str
    architecture_overview: str | None
    frontend_tasks: list[FileTask | str]
    backend_tasks: list[FileTask | str]
    planning_status: str | None
    frontend_artifacts: Annotated[list[FileArtifact], operator.add]
    frontend_status: str | None
    backend_artifacts: Annotated[list[FileArtifact], operator.add]
    backend_status: str | None
    validation_status: str | None
    test_results: dict[str, Any] | None
    errors: Annotated[list[str], operator.add]
    overall_status: str | None
