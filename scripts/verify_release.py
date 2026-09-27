"""Verify the archived Linux binary against the release manifest."""

import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
DIST = ROOT / "dist"


def git_value(*arguments):
    result = subprocess.run(
        ["git", *arguments], cwd=ROOT, text=True, encoding="utf-8",
        errors="replace", capture_output=True, check=True,
    )
    return result.stdout.strip()


def main():
    manifest = json.loads((DIST / "manifest.json").read_text(encoding="utf-8"))
    if manifest.get("schemaVersion") != 2 or manifest.get("project") != "cc-agent-go":
        raise ValueError("unexpected release manifest schema or project")
    source = manifest["source"]
    if source.get("commit") != git_value("rev-parse", "HEAD"):
        raise ValueError("manifest commit does not match Jenkins checkout")
    if source.get("commitTree") != git_value("rev-parse", "HEAD^{tree}"):
        raise ValueError("manifest tree does not match Jenkins checkout")
    if source.get("dirty") is not False:
        raise ValueError("release was built from a dirty source checkout")
    build_input = source.get("buildInputSha256")
    if not isinstance(build_input, str) or not re.fullmatch(r"[0-9a-f]{64}", build_input):
        raise ValueError("missing build input checksum")

    artifacts = manifest.get("artifacts")
    if not isinstance(artifacts, list) or len(artifacts) != 1:
        raise ValueError("expected exactly one runnable artifact")
    artifact = artifacts[0]
    if artifact.get("file") != "cc-agent-go":
        raise ValueError("expected the Linux cc-agent-go binary")
    binary = DIST / artifact["file"]
    if binary.read_bytes()[:4] != b"\x7fELF":
        raise ValueError("artifact is not a Linux ELF binary")
    if binary.stat().st_size != artifact.get("sizeBytes"):
        raise ValueError("artifact size does not match manifest")
    digest = hashlib.sha256(binary.read_bytes()).hexdigest()
    if digest != artifact.get("sha256"):
        raise ValueError("artifact SHA-256 does not match manifest")
    print("Release manifest and Linux binary: OK")


if __name__ == "__main__":
    try:
        main()
    except (OSError, KeyError, TypeError, ValueError, subprocess.CalledProcessError) as error:
        print(f"Release verification failed: {error}", file=sys.stderr)
        sys.exit(1)
