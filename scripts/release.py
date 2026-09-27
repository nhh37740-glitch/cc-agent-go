"""Build and describe the runnable cc-agent-go binary without extra packages."""

from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
DIST = ROOT / "dist"


def run(*args, check=True):
    return subprocess.run(
        args, cwd=ROOT, check=check, text=True, encoding="utf-8",
        errors="replace", capture_output=True,
    )


def git_value(*args):
    if not (ROOT / ".git").exists():
        return None
    result = run("git", *args, check=False)
    return result.stdout.strip() if result.returncode == 0 else None


def source_digest():
    tracked = run("git", "ls-files", "-co", "--exclude-standard", "-z", check=False) if (ROOT / ".git").exists() else None
    if tracked is not None and tracked.returncode == 0:
        names = [Path(name) for name in tracked.stdout.split("\0") if name]
    else:
        excluded = {"dist", ".git", ".cache", ".gocache", "logs", "workspace"}
        names = [
            path.relative_to(ROOT) for path in ROOT.rglob("*")
            if path.is_file() and not any(part in excluded for part in path.relative_to(ROOT).parts)
        ]
    digest = hashlib.sha256()
    for name in sorted(names, key=lambda path: path.as_posix()):
        path = ROOT / name
        if path.is_file():
            digest.update(name.as_posix().encode("utf-8") + b"\0")
            digest.update(hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def main():
    commit = git_value("rev-parse", "HEAD") or os.environ.get("SOURCE_COMMIT")
    commit_tree = git_value("rev-parse", "HEAD^{tree}") or os.environ.get("SOURCE_TREE")
    status = git_value("status", "--porcelain", "--untracked-files=normal")
    dirty = bool(status) if status is not None else (
        os.environ.get("SOURCE_DIRTY", "").lower() == "true" if commit else None
    )
    tree_digest = source_digest()

    for command in (
        [sys.executable, "scripts/check_boundaries.py"],
        # Tokenizer fixtures mmap a large model file; serialize packages so a
        # small Jenkins/Docker worker does not run several heavy test processes.
        ["go", "test", "-p=1", "./..."],
        ["go", "vet", "-p=1", "./..."],
    ):
        print("+", " ".join(command), flush=True)
        result = run(*command, check=False)
        print(result.stdout, end="")
        print(result.stderr, end="", file=sys.stderr)
        if result.returncode:
            return result.returncode

    DIST.mkdir(exist_ok=True)
    binary_name = "cc-agent-go.exe" if os.name == "nt" else "cc-agent-go"
    binary = DIST / binary_name
    command = ["go", "build", "-trimpath", "-o", str(binary), "."]
    print("+", " ".join(command), flush=True)
    result = run(*command, check=False)
    print(result.stdout, end="")
    print(result.stderr, end="", file=sys.stderr)
    if result.returncode:
        return result.returncode

    manifest = {
        "schemaVersion": 1,
        "project": "cc-agent-go",
        "builtAtUtc": datetime.now(timezone.utc).isoformat(),
        "source": {
            "commit": commit,
            "commitTree": commit_tree,
            "dirty": dirty,
            "workingTreeSha256": tree_digest,
        },
        "toolchain": {"go": run("go", "version").stdout.strip()},
        "artifacts": [{
            "file": binary_name,
            "sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
            "sizeBytes": binary.stat().st_size,
        }],
    }
    (DIST / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    print(f"Created {binary} and manifest.json")
    return 0


if __name__ == "__main__":
    sys.exit(main())
