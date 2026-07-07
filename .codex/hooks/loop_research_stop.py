#!/usr/bin/env python3
"""Codex Stop hook for Billyharness loop-research iterations."""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import hashlib
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


PROJECT = "billyharness"
STATE_REL = Path("loop-research/.hook-state.json")
ITERATIONS_REL = Path("loop-research/iterations")
ENABLE_MARKER = "LOOP_RESEARCH_ENABLE"
ITERATION_DONE_RE = re.compile(r"(?m)^ITERATION_DONE:\s*(.+?)\s*$")
RESULT_DONE_RE = re.compile(r"(?m)^LOOP_RESEARCH_RESULT_DONE:\s*(.+?)\s*$")
MAX_TRANSCRIPT_HEAD = 1_000_000
MAX_TRANSCRIPT_TAIL = 2_000_000
MAX_PROCESSED_TURNS = 200
MAX_PROCESSED_ITERATIONS = 500
DEFAULT_TARGET = 15


def main() -> int:
    try:
        payload = read_payload()
        if payload.get("hook_event_name") != "Stop":
            return 0

        root = git_root(Path(payload.get("cwd") or os.getcwd()))
        if root is None or not is_project_root(root):
            return 0

        state_path = root / STATE_REL
        state = read_json_file(state_path)
        if not state.get("enabled"):
            return 0

        session_id = str(payload.get("session_id") or "")
        turn_id = str(payload.get("turn_id") or "")
        if not session_id or not turn_id:
            return 0

        bound_session = str(state.get("session_id") or "")
        transcript = read_transcript(payload.get("transcript_path"))
        loop_id = str(state.get("loop_id") or "")
        if bound_session and bound_session != session_id:
            if not transcript_matches_loop(transcript, loop_id, state):
                return 0
            previous = list_of_strings(state.get("previous_session_ids"))
            previous.append(bound_session)
            state["previous_session_ids"] = previous[-20:]
            state["session_id"] = session_id
            state["status"] = "running"
            state["rebound_at"] = now()
            bound_session = session_id

        if not bound_session:
            if not transcript_matches_loop(transcript, loop_id, state):
                return 0
            state["session_id"] = session_id
            state["status"] = "running"
            state["bound_at"] = now()

        target = positive_int(state.get("target"), DEFAULT_TARGET)
        iterations_path = root / ITERATIONS_REL
        current_count = count_stars(iterations_path)
        completed = max(positive_int(state.get("completed_iterations"), 0), current_count)
        processed = list_of_strings(state.get("processed_turn_ids"))
        processed_keys = list_of_strings(state.get("processed_iteration_keys"))
        if not processed_keys and state.get("last_finding"):
            processed_keys.append(finding_key(str(state["last_finding"])))
        last_message = str(payload.get("last_assistant_message") or "")

        if completed >= target:
            return handle_target_reached(root, state_path, state, last_message, completed, target)

        done = extract_match(ITERATION_DONE_RE, last_message)
        recorded = False
        done_key = finding_key(done) if is_real_finding(done) else ""
        if done_key and done_key not in processed_keys:
            completed += 1
            processed.append(turn_id)
            processed_keys.append(done_key)
            state["last_finding"] = done[:500]
            write_iterations(iterations_path, completed)
            append_raw_iteration(root, state, completed, done, last_message, payload)
            recorded = True

        recovered = recover_transcript_findings(payload.get("transcript_path"), processed_keys, target - completed)
        if recovered:
            for finding, text in recovered:
                completed += 1
                processed_keys.append(finding_key(finding))
                state["last_finding"] = finding[:500]
                append_raw_iteration(root, state, completed, finding, text, payload)
            write_iterations(iterations_path, completed)

        state["completed_iterations"] = completed
        state["processed_turn_ids"] = processed[-MAX_PROCESSED_TURNS:]
        state["processed_iteration_keys"] = processed_keys[-MAX_PROCESSED_ITERATIONS:]
        state["last_seen_at"] = now()
        state["transcript_path"] = payload.get("transcript_path")

        if completed >= target:
            write_json_file(state_path, state)
            emit_block(
                "Loop research reached "
                f"{completed}/{target} iterations. Write the final report to "
                f"{result_path(state)} by compacting {raw_path(state)}, then end with "
                f"LOOP_RESEARCH_RESULT_DONE: {result_path(state)}."
            )
            return 0

        write_json_file(state_path, state)
        if recorded:
            reason = (
                f"Recorded loop-research iteration {completed}/{target}: {done[:240]}. "
                f"Saved the raw iteration body to {raw_path(state)}. Continue with the "
                "next distinct research angle. Do not edit loop-research/iterations "
                f"or {raw_path(state)}. End the next completed iteration with "
                "ITERATION_DONE: <one concrete finding>."
            )
        elif recovered:
            reason = (
                f"Recovered {len(recovered)} loop-research iteration(s) from transcript; "
                f"counter is now {completed}/{target}. Continue with the next distinct "
                "research angle. Do not edit loop-research/iterations. End the next "
                "completed iteration with ITERATION_DONE: <one concrete finding>."
            )
        elif done_key:
            reason = (
                f"Loop research is still at {completed}/{target}; this finding was already "
                "counted. Continue with a new distinct research angle. Do not edit "
                "loop-research/iterations. End the next completed iteration with "
                "ITERATION_DONE: <one concrete finding>."
            )
        elif done:
            reason = (
                f"Loop research is armed at {completed}/{target}, but the ITERATION_DONE "
                "marker did not contain a real finding. Complete one distinct, "
                "evidence-backed research iteration. Do not edit loop-research/iterations. "
                "End with ITERATION_DONE: <one concrete finding>."
            )
        else:
            reason = (
                f"Loop research is armed at {completed}/{target}, but this turn did not "
                "end with ITERATION_DONE. Do not stop and do not answer with meta-commentary. "
                f"Read {prompt_path(state)} if the task context is unclear, then perform "
                f"iteration {completed + 1}: inspect a distinct evidence source, write the "
                "finding/evidence/impact/test shape, and make the final line exactly "
                "ITERATION_DONE: <one concrete finding>. Do not edit loop-research/iterations; "
                f"the Stop hook records counted iterations into {raw_path(state)}."
            )
        emit_block(reason)
        return 0
    except Exception as exc:
        emit_json({"systemMessage": f"Loop research hook skipped after internal error: {exc}"})
        return 0


def handle_target_reached(root: Path, state_path: Path, state: dict[str, Any], last_message: str, completed: int, target: int) -> int:
    result_done = extract_match(RESULT_DONE_RE, last_message)
    result = root / result_path(state)
    if result_done and result.exists() and result.stat().st_size > 0:
        state["enabled"] = False
        state["status"] = "complete"
        state["completed_iterations"] = completed
        state["completed_at"] = now()
        write_json_file(state_path, state)
        emit_json({"systemMessage": f"Loop research complete: {completed}/{target} iterations and {result_path(state)} written."})
        return 0

    state["completed_iterations"] = completed
    state["last_seen_at"] = now()
    write_json_file(state_path, state)
    emit_block(
        f"Loop research has {completed}/{target} iterations. Write the final report to "
        f"{result_path(state)} by compacting {raw_path(state)} before stopping. End the turn with "
        f"LOOP_RESEARCH_RESULT_DONE: {result_path(state)}."
    )
    return 0


def read_payload() -> dict[str, Any]:
    raw = sys.stdin.read()
    if not raw.strip():
        return {}
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        return {}
    return value if isinstance(value, dict) else {}


def git_root(start: Path) -> Path | None:
    try:
        completed = subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            cwd=str(start),
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=5,
            check=False,
        )
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return None
    if completed.returncode != 0:
        return None
    text = completed.stdout.strip()
    return Path(text) if text else None


def is_project_root(root: Path) -> bool:
    return root.name == PROJECT and (root / "AGENTS.md").exists() and (root / "llms.txt").exists()


def read_json_file(path: Path) -> dict[str, Any]:
    if not path.exists():
        return {}
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {}
    return value if isinstance(value, dict) else {}


def write_json_file(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    tmp.replace(path)


def read_transcript(value: Any) -> str:
    if not value:
        return ""
    path = Path(str(value))
    try:
        size = path.stat().st_size
        with path.open("rb") as handle:
            if size <= MAX_TRANSCRIPT_HEAD + MAX_TRANSCRIPT_TAIL:
                data = handle.read()
            else:
                head = handle.read(MAX_TRANSCRIPT_HEAD)
                handle.seek(max(0, size - MAX_TRANSCRIPT_TAIL))
                tail = handle.read(MAX_TRANSCRIPT_TAIL)
                data = head + b"\n...\n" + tail
    except OSError:
        return ""
    return data.decode("utf-8", errors="replace")


def count_stars(path: Path) -> int:
    try:
        return path.read_text(encoding="utf-8").count("*")
    except OSError:
        return 0


def write_iterations(path: Path, count: int) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("*" * max(0, count) + "\n", encoding="utf-8")


def positive_int(value: Any, default: int) -> int:
    try:
        number = int(value)
    except (TypeError, ValueError):
        return default
    return number if number >= 0 else default


def list_of_strings(value: Any) -> list[str]:
    if not isinstance(value, list):
        return []
    return [str(item) for item in value if str(item)]


def extract_match(pattern: re.Pattern[str], text: str) -> str:
    matches = pattern.findall(text or "")
    return matches[-1].strip() if matches else ""


def recover_transcript_findings(value: Any, processed_keys: list[str], limit: int) -> list[tuple[str, str]]:
    if limit <= 0 or not value:
        return []
    path = Path(str(value))
    seen = set(processed_keys)
    recovered: list[tuple[str, str]] = []
    try:
        with path.open("r", encoding="utf-8", errors="replace") as handle:
            for line in handle:
                try:
                    item = json.loads(line)
                except json.JSONDecodeError:
                    continue
                for text in assistant_texts(item):
                    for finding in ITERATION_DONE_RE.findall(text):
                        finding = finding.strip()
                        if not is_real_finding(finding):
                            continue
                        key = finding_key(finding)
                        if key in seen:
                            continue
                        seen.add(key)
                        recovered.append((finding, text))
                        if len(recovered) >= limit:
                            return recovered
    except OSError:
        return []
    return recovered


def assistant_texts(item: dict[str, Any]) -> list[str]:
    if item.get("type") == "event_msg":
        payload = item.get("payload")
        if (
            isinstance(payload, dict)
            and payload.get("type") == "agent_message"
            and isinstance(payload.get("message"), str)
        ):
            return [payload["message"]]
        return []
    if item.get("type") != "response_item":
        return []
    payload = item.get("payload")
    if not isinstance(payload, dict) or payload.get("type") != "message":
        return []
    if payload.get("role") != "assistant":
        return []
    texts: list[str] = []
    content = payload.get("content")
    if not isinstance(content, list):
        return texts
    for part in content:
        if isinstance(part, dict) and isinstance(part.get("text"), str):
            texts.append(part["text"])
    return texts


def transcript_matches_loop(transcript: str, loop_id: str, state: dict[str, Any]) -> bool:
    if not transcript_has_activation_user_message(transcript, loop_id):
        return False
    prompt_path = str(state.get("prompt_path") or "")
    result_path = str(state.get("result_path") or "")
    if prompt_path and prompt_path not in transcript:
        return False
    if result_path and result_path not in transcript:
        return False
    return True


def transcript_has_activation_user_message(transcript: str, loop_id: str) -> bool:
    if not transcript or not loop_id:
        return False
    loop_re = re.compile(rf"(?m)^LOOP_RESEARCH_ID:\s*{re.escape(loop_id)}\s*$")
    for line in transcript.splitlines():
        try:
            item = json.loads(line)
        except json.JSONDecodeError:
            continue
        for text in user_texts(item):
            if ENABLE_MARKER in text and loop_re.search(text):
                return True
    return False


def user_texts(item: dict[str, Any]) -> list[str]:
    if item.get("type") == "event_msg":
        payload = item.get("payload")
        if (
            isinstance(payload, dict)
            and payload.get("type") == "user_message"
            and isinstance(payload.get("message"), str)
        ):
            return [payload["message"]]
        return []
    if item.get("type") != "response_item":
        return []
    payload = item.get("payload")
    if not isinstance(payload, dict) or payload.get("type") != "message":
        return []
    if payload.get("role") != "user":
        return []
    texts: list[str] = []
    content = payload.get("content")
    if not isinstance(content, list):
        return texts
    for part in content:
        if isinstance(part, dict) and isinstance(part.get("text"), str):
            texts.append(part["text"])
    return texts


def is_real_finding(value: str) -> bool:
    text = " ".join((value or "").strip().split())
    if not text:
        return False
    lowered = text.lower()
    if "one concrete finding" in lowered:
        return False
    if text.startswith("<") or text.startswith("&lt;"):
        return False
    return len(text) >= 20


def finding_key(value: str) -> str:
    normalized = " ".join((value or "").strip().lower().split())
    return hashlib.sha256(normalized.encode("utf-8")).hexdigest()


def result_path(state: dict[str, Any]) -> str:
    value = str(state.get("result_path") or "").strip()
    return value if value else "loop-research/NNN-result.md"


def raw_path(state: dict[str, Any]) -> str:
    value = str(state.get("raw_path") or "").strip()
    if value:
        return value
    result = result_path(state)
    if result.endswith("-result.md"):
        return result[: -len("-result.md")] + "-raw.md"
    if result.endswith(".md"):
        return result[:-3] + "-raw.md"
    return "loop-research/NNN-raw.md"


def prompt_path(state: dict[str, Any]) -> str:
    value = str(state.get("prompt_path") or "").strip()
    return value if value else "loop-research/NNN-prompt.md"


def append_raw_iteration(root: Path, state: dict[str, Any], number: int, finding: str, text: str, payload: dict[str, Any]) -> None:
    path = root / raw_path(state)
    path.parent.mkdir(parents=True, exist_ok=True)
    clean_text = (text or "").rstrip()
    if not clean_text:
        clean_text = f"ITERATION_DONE: {finding}"
    try:
        needs_header = not path.exists() or path.stat().st_size == 0
    except OSError:
        needs_header = True
    file_header = (
        f"# Loop Research Raw Log: {state.get('loop_id') or ''}\n\n"
        f"- prompt_path: {prompt_path(state)}\n"
        f"- result_path: {result_path(state)}\n"
        f"- target: {positive_int(state.get('target'), DEFAULT_TARGET)}\n"
        "- owner: Stop hook append-only log; do not edit during an active loop.\n"
    )
    header = (
        f"\n\n## Iteration {number}\n\n"
        f"- recorded_at: {now()}\n"
        f"- session_id: {payload.get('session_id') or ''}\n"
        f"- turn_id: {payload.get('turn_id') or ''}\n"
        f"- finding: {finding}\n\n"
        "### Raw Assistant Message\n\n"
    )
    with path.open("a", encoding="utf-8") as handle:
        if needs_header:
            handle.write(file_header)
        handle.write(header if not needs_header else header.lstrip())
        handle.write(clean_text)
        handle.write("\n")


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


def emit_block(reason: str) -> None:
    emit_json({"decision": "block", "reason": reason})


def emit_json(value: dict[str, Any]) -> None:
    print(json.dumps(value, ensure_ascii=False))


if __name__ == "__main__":
    raise SystemExit(main())
