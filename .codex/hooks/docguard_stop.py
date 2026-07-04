#!/usr/bin/env python3
"""Fast Codex Stop hook documentation guard for billyharness."""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from pathlib import Path


PROJECT = "billyharness"
MANIFEST = "agent-index/docs-manifest.json"
ACTIVE_ENV = "BILLYHARNESS_DOCGUARD_STOP_ACTIVE"
ALLOWED_ACTIVE_WORK_DOCS = {
    "docs/README.md",
    "docs/documentation-system.md",
    "docs/research/README.md",
}
GIT_TIMEOUT = 8
GO_TIMEOUT = 25
MAX_PATHS = 8
MAX_LINES = 6

ACTIVE_WORK_PATTERNS = [
    re.compile(pattern, re.IGNORECASE)
    for pattern in (
        r"copy-ready\s+Codex\s+/goal",
        r"/goal\s+prompt",
        r"implementation\s+checklist\b",
        r"active\s+TODO\b",
        r"current\s+TODO\b",
        r"^\s{0,3}(?:#+\s*)?(?:[-*]\s*)?(?:\*\*)?completion\s+evidence\b",
        r"^\s{0,3}(?:#+\s*)?(?:[-*]\s*)?(?:\*\*)?commands\s+run\b\s*:?",
        r"^\s{0,3}(?:#+\s*)?(?:[-*]\s*)?(?:\*\*)?remaining\s+blockers\b",
        r"^\s*-\s+\[[ xX]\]",
    )
]


class CommandResult:
    def __init__(self, code: int, stdout: str = "", stderr: str = "") -> None:
        self.code = code
        self.stdout = stdout
        self.stderr = stderr


def main() -> int:
    try:
        payload = read_payload()
        if hook_is_active(payload):
            return 0

        root = git_root(Path(payload.get("cwd") or os.getcwd()))
        if root is None or not is_project_root(root):
            return 0

        tracked = git_lines(root, ["diff", "--name-only", "--diff-filter=ACMRTUXB", "HEAD", "--"])
        untracked = git_lines(root, ["ls-files", "--others", "--exclude-standard"])
        changed = unique_paths(tracked + untracked)
        relevant = [path for path in changed if is_relevant(path)]
        if not relevant:
            return 0

        issues: list[str] = []
        warnings: list[str] = []

        issues.extend(check_manifest(root))
        issues.extend(check_diff_whitespace(root, tracked))
        issues.extend(check_active_work_language(root, changed, set(untracked)))

        run_architecture = should_run_architecture_guard(root, changed, set(untracked))
        if run_architecture:
            arch_issues, arch_warnings = run_architecture_guard(root)
            issues.extend(arch_issues)
            warnings.extend(arch_warnings)

        docs_or_evidence = any(is_doc_surface(path) or is_active_work_surface(path) for path in changed)
        sensitive = [path for path in changed if is_docs_sensitive_change(path)]
        if sensitive and not docs_or_evidence:
            issues.append(
                "Docs-sensitive changes lack docs/index/loop evidence: "
                f"{short_paths(sensitive)}. Check llms.txt, .agents/rules/README.md, "
                "docs/README.md, docs/documentation-system.md, and "
                "agent-index/docs-manifest.json; update docs or record docs-not-needed evidence."
            )

        if issues:
            rerun = "git diff --check"
            if run_architecture:
                rerun += " && go test -count=1 ./internal/architecture"
            emit_json(
                {
                    "decision": "block",
                    "reason": "Documentation drift check found issues. "
                    + " ".join(issues[:MAX_LINES])
                    + f" Rerun: {rerun}.",
                }
            )
        elif warnings:
            emit_json({"systemMessage": " ".join(warnings[:MAX_LINES])})
        return 0
    except Exception as exc:  # Soft guard: never fail the turn on hook bugs.
        emit_json({"systemMessage": f"Docguard skipped after internal error: {exc}"})
        return 0


def read_payload() -> dict:
    raw = sys.stdin.read()
    if not raw.strip():
        return {}
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        return {}
    return value if isinstance(value, dict) else {}


def hook_is_active(payload: dict) -> bool:
    if os.environ.get(ACTIVE_ENV) == "1":
        return True
    return bool(payload.get("stop_hook_active"))


def git_root(start: Path) -> Path | None:
    result = run(["git", "rev-parse", "--show-toplevel"], cwd=start, timeout=GIT_TIMEOUT)
    if result.code != 0:
        return None
    text = result.stdout.strip()
    return Path(text) if text else None


def is_project_root(root: Path) -> bool:
    return root.name == PROJECT and (root / "AGENTS.md").exists() and (root / "llms.txt").exists()


def git_lines(root: Path, args: list[str]) -> list[str]:
    result = run(["git", *args], cwd=root, timeout=GIT_TIMEOUT)
    if result.code != 0:
        return []
    return [line.strip() for line in result.stdout.splitlines() if line.strip()]


def run(args: list[str], cwd: Path, timeout: int) -> CommandResult:
    try:
        completed = subprocess.run(
            args,
            cwd=str(cwd),
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=timeout,
            check=False,
        )
    except FileNotFoundError as exc:
        return CommandResult(127, "", str(exc))
    except subprocess.TimeoutExpired as exc:
        return CommandResult(124, exc.stdout or "", exc.stderr or f"{args[0]} timed out")
    return CommandResult(completed.returncode, completed.stdout, completed.stderr)


def unique_paths(paths: list[str]) -> list[str]:
    seen: set[str] = set()
    out: list[str] = []
    for path in paths:
        normalized = path.strip().replace("\\", "/")
        if normalized and normalized not in seen:
            seen.add(normalized)
            out.append(normalized)
    return out


def is_relevant(path: str) -> bool:
    return (
        is_doc_surface(path)
        or is_active_work_surface(path)
        or path.startswith("cmd/")
        or path.startswith("internal/")
        or path.startswith("ops/")
        or path in {"go.mod", "go.sum", ".codex/hooks.json"}
        or path.startswith(".codex/hooks/")
    )


def is_doc_surface(path: str) -> bool:
    return (
        path in {"AGENTS.md", "README.md", "llms.txt", MANIFEST}
        or (path.startswith(".agents/rules/") and path.endswith(".md"))
        or (path.startswith("docs/") and path.endswith(".md"))
        or (path.startswith("agent-index/") and (path.endswith(".md") or path.endswith(".json")))
    )


def is_active_work_surface(path: str) -> bool:
    return (
        path.startswith("loop-develop/current-todo/") and path.endswith(".md")
    ) or (
        path.startswith("loop-develop/history/") and path.endswith(".md")
    )


def is_docs_sensitive_change(path: str) -> bool:
    if path.endswith("_test.go"):
        return False
    return (
        (path.startswith("cmd/") and path.endswith(".go"))
        or (path.startswith("internal/") and path.endswith(".go"))
        or path in {"README.md", "go.mod", "go.sum"}
        or path.startswith("ops/")
        or path.startswith(".agents/rules/")
        or path.startswith("agent-index/")
    )


def short_paths(paths: list[str]) -> str:
    head = paths[:MAX_PATHS]
    suffix = "" if len(paths) <= MAX_PATHS else f", +{len(paths) - MAX_PATHS} more"
    return ", ".join(head) + suffix


def check_manifest(root: Path) -> list[str]:
    path = root / MANIFEST
    if not path.exists():
        return []
    try:
        with path.open("r", encoding="utf-8") as handle:
            json.load(handle)
    except json.JSONDecodeError as exc:
        return [f"{MANIFEST} is invalid JSON: line {exc.lineno} column {exc.colno}."]
    except OSError as exc:
        return [f"Could not read {MANIFEST}: {exc}."]
    return []


def check_diff_whitespace(root: Path, tracked: list[str]) -> list[str]:
    if not tracked:
        return []
    result = run(["git", "diff", "--check", "--", *tracked], cwd=root, timeout=GIT_TIMEOUT)
    if result.code == 0:
        return []
    text = useful_lines(result.stdout + "\n" + result.stderr)
    return [f"git diff --check failed: {'; '.join(text)}."]


def check_active_work_language(root: Path, changed: list[str], untracked: set[str]) -> list[str]:
    findings: list[str] = []
    for path in changed:
        if not path.startswith("docs/") or not path.endswith(".md"):
            continue
        if path in ALLOWED_ACTIVE_WORK_DOCS:
            continue
        added = untracked_added_lines(root, path) if path in untracked else diff_added_lines(root, path)
        fenced = fenced_line_numbers(root / path)
        for line_no, text in added:
            if line_no in fenced:
                continue
            if matches_active_work(text):
                findings.append(f"{path}:{line_no} adds active-work language: {text.strip()[:100]}")
                break
    if not findings:
        return []
    return ["Active-work language belongs outside docs/: " + "; ".join(findings[:MAX_LINES]) + "."]


def diff_added_lines(root: Path, path: str) -> list[tuple[int, str]]:
    result = run(["git", "diff", "--unified=0", "--", path], cwd=root, timeout=GIT_TIMEOUT)
    if result.code != 0 or not result.stdout:
        return []
    added: list[tuple[int, str]] = []
    new_line = 0
    for raw in result.stdout.splitlines():
        if raw.startswith("@@"):
            match = re.search(r"\+(\d+)(?:,(\d+))?", raw)
            new_line = int(match.group(1)) - 1 if match else 0
            continue
        if raw.startswith(("+++", "---")):
            continue
        if raw.startswith("+"):
            new_line += 1
            added.append((new_line, raw[1:]))
        elif raw.startswith("-"):
            continue
        else:
            new_line += 1
    return added


def untracked_added_lines(root: Path, path: str) -> list[tuple[int, str]]:
    full = root / path
    try:
        lines = full.read_text(encoding="utf-8", errors="ignore").splitlines()
    except OSError:
        return []
    return [(idx, text) for idx, text in enumerate(lines, start=1)]


def fenced_line_numbers(path: Path) -> set[int]:
    try:
        lines = path.read_text(encoding="utf-8", errors="ignore").splitlines()
    except OSError:
        return set()
    fenced: set[int] = set()
    in_fence = False
    for idx, text in enumerate(lines, start=1):
        if text.lstrip().startswith("```"):
            fenced.add(idx)
            in_fence = not in_fence
            continue
        if in_fence:
            fenced.add(idx)
    return fenced


def matches_active_work(text: str) -> bool:
    return any(pattern.search(text) for pattern in ACTIVE_WORK_PATTERNS)


def should_run_architecture_guard(root: Path, changed: list[str], untracked: set[str]) -> bool:
    if "docs/architecture.md" in changed:
        return True
    for path in changed:
        if not path.endswith(".go") or not (path.startswith("internal/") or path.startswith("cmd/")):
            continue
        if path in untracked:
            if untracked_go_has_package_or_internal_import(root, path):
                return True
            continue
        if tracked_go_diff_has_package_or_internal_import(root, path):
            return True
    return False


def tracked_go_diff_has_package_or_internal_import(root: Path, path: str) -> bool:
    result = run(["git", "diff", "--unified=0", "--", path], cwd=root, timeout=GIT_TIMEOUT)
    if result.code != 0:
        return False
    for raw in result.stdout.splitlines():
        if not raw.startswith(("+", "-")) or raw.startswith(("+++", "---")):
            continue
        text = raw[1:].strip()
        if is_package_or_import_line(text):
            return True
    return False


def untracked_go_has_package_or_internal_import(root: Path, path: str) -> bool:
    full = root / path
    try:
        lines = full.read_text(encoding="utf-8", errors="ignore").splitlines()
    except OSError:
        return False
    return any(is_package_or_import_line(line.strip()) for line in lines[:200])


def is_package_or_import_line(text: str) -> bool:
    return (
        text.startswith("package ")
        or text.startswith("import ")
        or bool(re.match(r'^(?:[._A-Za-z0-9]+\s+)?"[^"]+"$', text))
        or '"billyharness/internal/' in text
    )


def run_architecture_guard(root: Path) -> tuple[list[str], list[str]]:
    result = run(["go", "test", "-count=1", "./internal/architecture"], cwd=root, timeout=GO_TIMEOUT)
    if result.code == 0:
        return [], []
    if result.code == 127:
        return [], ["Docguard skipped architecture guard because go was not found."]
    text = useful_lines(result.stdout + "\n" + result.stderr)
    return [f"go test -count=1 ./internal/architecture failed: {'; '.join(text)}."], []


def useful_lines(text: str) -> list[str]:
    lines = [line.strip() for line in text.splitlines() if line.strip()]
    return lines[:MAX_LINES] or ["no output"]


def emit_json(value: dict) -> None:
    sys.stdout.write(json.dumps(value, separators=(",", ":"), ensure_ascii=False))


if __name__ == "__main__":
    raise SystemExit(main())
