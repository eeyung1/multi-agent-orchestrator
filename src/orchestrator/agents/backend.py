import os
import time
from typing import List, Dict, Any
from pydantic import BaseModel, Field
from langchain_core.prompts import ChatPromptTemplate
from orchestrator.state import ProjectState
from orchestrator.model_factory import get_chat_model

class SingleFileArtifact(BaseModel):
    filepath: str = Field(description="Relative filepath for the code artifact, e.g. 'main.go' or 'asciiart/banner.go'")
    content: str = Field(description="Complete, raw file content with no markdown backtick fences")
    description: str = Field(description="Brief explanation of this file")

SINGLE_FILE_PROMPT = ChatPromptTemplate.from_messages([
    ("system", (
        "You are an expert Backend Engineer. You are tasked with implementing ONE specific file for a project.\n"
        "Output ONLY the complete, production-ready code for the requested file.\n"
        "Do not leave placeholders or TODOs. Do not wrap code in markdown fences inside content."
    )),
    ("human", (
        "Overall Architecture:\n{architecture}\n\n"
        "Current Task to implement:\n{current_task}\n\n"
        "Provide the implementation for this single file."
    ))
])

def backend_node(state: ProjectState) -> dict:
    backend_tasks = state.get("backend_tasks") or []
    if not backend_tasks:
        return {"backend_status": "completed", "backend_artifacts": []}

    workspace = os.environ.get("TARGET_WORKSPACE_DIR") or state.get("target_workspace") or "."
    architecture = state.get("architecture_overview") or "Standard Go HTTP server"
    
    llm = get_chat_model(role="backend", temperature=0.1)
    structured_llm = llm.with_structured_output(SingleFileArtifact)

    artifacts: List[Dict[str, Any]] = []
    errors: List[str] = []

    for task in backend_tasks:
        task_str = str(task)
        success = False
        attempts = 0
        max_attempts = 4

        while not success and attempts < max_attempts:
            attempts += 1
            try:
                prompt_input = {
                    "architecture": architecture,
                    "current_task": task_str,
                }
                chain = SINGLE_FILE_PROMPT | structured_llm
                result: SingleFileArtifact = chain.invoke(prompt_input)

                if result and result.filepath and result.content:
                    clean_path = result.filepath.lstrip("/\\")
                    full_path = os.path.join(workspace, clean_path)
                    os.makedirs(os.path.dirname(full_path), exist_ok=True)
                    with open(full_path, "w", encoding="utf-8") as f:
                        f.write(result.content)

                    artifacts.append({
                        "filepath": clean_path,
                        "content": result.content,
                        "description": result.description
                    })
                    print(f"  ✓ [Backend] Generated: {clean_path}")
                    success = True

                # Pace calls to respect per-minute ceilings
                time.sleep(3.0)
            except Exception as e:
                err_text = str(e)
                if "429" in err_text or "rate_limit" in err_text:
                    print(f"  ⏳ [Backend] Rate limit hit. Pausing 15s before retry (attempt {attempts}/{max_attempts})...")
                    time.sleep(15.0)
                else:
                    err_msg = f"Backend task failed ({task_str[:40]}): {err_text[:80]}"
                    errors.append(err_msg)
                    print(f"  ✗ [Backend] {err_msg}")
                    break

    status = "completed" if artifacts else "failed"
    return {
        "backend_artifacts": artifacts,
        "backend_status": status,
        "errors": errors
    }
