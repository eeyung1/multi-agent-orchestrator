import os
import time
from typing import List, Dict, Any
from pydantic import BaseModel, Field
from langchain_core.prompts import ChatPromptTemplate
from orchestrator.state import ProjectState
from orchestrator.model_factory import get_chat_model

class SingleFileArtifact(BaseModel):
    filepath: str = Field(description="Relative filepath for the frontend artifact, e.g. 'templates/index.html' or 'static/style.css'")
    content: str = Field(description="Complete, raw file content with no markdown backtick fences")
    description: str = Field(description="Brief explanation of this file")

SINGLE_FILE_PROMPT = ChatPromptTemplate.from_messages([
    ("system", (
        "You are an expert Frontend Engineer. You are tasked with implementing ONE specific frontend template or stylesheet.\n"
        "Output ONLY the complete, production-ready code for the requested file.\n"
        "Do not leave placeholders or TODOs. Do not wrap code in markdown fences inside content."
    )),
    ("human", (
        "Overall Architecture:\n{architecture}\n\n"
        "Current Task to implement:\n{current_task}\n\n"
        "Provide the implementation for this single file."
    ))
])

def frontend_node(state: ProjectState) -> dict:
    frontend_tasks = state.get("frontend_tasks") or []
    if not frontend_tasks:
        return {"frontend_status": "completed", "frontend_artifacts": []}

    workspace = os.environ.get("TARGET_WORKSPACE_DIR") or state.get("target_workspace") or "."
    architecture = state.get("architecture_overview") or "Standard Go HTML templates"
    
    llm = get_chat_model(role="frontend", temperature=0.1)
    structured_llm = llm.with_structured_output(SingleFileArtifact)

    artifacts: List[Dict[str, Any]] = []
    errors: List[str] = []

    for task in frontend_tasks:
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
                    print(f"  ✓ [Frontend] Generated: {clean_path}")
                    success = True

                time.sleep(3.0)
            except Exception as e:
                err_text = str(e)
                if "429" in err_text or "rate_limit" in err_text:
                    print(f"  ⏳ [Frontend] Rate limit hit. Pausing 15s before retry (attempt {attempts}/{max_attempts})...")
                    time.sleep(15.0)
                else:
                    err_msg = f"Frontend task failed ({task_str[:40]}): {err_text[:80]}"
                    errors.append(err_msg)
                    print(f"  ✗ [Frontend] {err_msg}")
                    break

    status = "completed" if artifacts else "failed"
    return {
        "frontend_artifacts": artifacts,
        "frontend_status": status,
        "errors": errors
    }
