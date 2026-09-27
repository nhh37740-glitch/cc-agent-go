"""Keep the reusable Agent core independent of application orchestration."""

from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
ALLOWED = {
    "model": set(),
    "config": set(),
    "tool": set(),
    "memory": {"model"},
    "modeltoken": {"agent", "config"},
    "agent": {"model", "memory", "tool"},
    "mcp": {"tool"},
    "host": {"agent"},
    "service": {"agent", "config", "memory", "model", "modeltoken", "tool"},
    "harness": {"agent", "config", "memory", "model", "modeltoken", "service", "tool"},
}
IMPORT = re.compile(r'"cc-agent-go/([a-z][a-z0-9]*)[^"]*"')

violations = []
for package, allowed_dependencies in ALLOWED.items():
    for source in (ROOT / package).rglob("*.go"):
        if source.name.endswith("_test.go"):
            continue
        for dependency in IMPORT.findall(source.read_text(encoding="utf-8")):
            if dependency not in allowed_dependencies:
                violations.append(f"{source.relative_to(ROOT)} imports {dependency}")

if violations:
    print("Dependency boundary violations:\n" + "\n".join(violations), file=sys.stderr)
    sys.exit(1)
print("Go package dependency boundaries: OK")
