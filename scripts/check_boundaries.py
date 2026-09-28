"""Keep reusable Go packages decoupled from application orchestration."""

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
    "httpapi": {"agent", "config", "harness", "mcp", "memory", "model", "service", "tool", "webkey"},
}
IMPORT = re.compile(r'"cc-agent-go/([a-z][a-z0-9]*)[^"]*"')
ROOT_IMPORT = re.compile(r'"cc-agent-go"')

violations = []
for package, allowed_dependencies in ALLOWED.items():
    for source in (ROOT / package).rglob("*.go"):
        if source.name.endswith("_test.go"):
            continue
        text = source.read_text(encoding="utf-8")
        for dependency in IMPORT.findall(text):
            if dependency not in allowed_dependencies:
                violations.append(f"{source.relative_to(ROOT)} imports {dependency}")
        if package == "httpapi" and ROOT_IMPORT.search(text):
            violations.append(f"{source.relative_to(ROOT)} imports the composition root")

entrypoint = ROOT / "main.go"
entrypoint_source = entrypoint.read_text(encoding="utf-8")
if '"cc-agent-go/httpapi"' not in entrypoint_source:
    violations.append("main.go must construct the HTTP API package")
if re.search(r"\bhttp\.HandleFunc\s*\(|\bhttp\.DefaultServeMux\b", entrypoint_source):
    violations.append("main.go must not own HTTP route registration")
if re.search(r"(?m)^func\s+(?:handle[A-Z]|writeAPIError|writeSSEError)\w*\s*\(", entrypoint_source):
    violations.append("main.go must not define HTTP handlers")

if violations:
    print("Go package dependency boundary violations:\n" + "\n".join(violations), file=sys.stderr)
    sys.exit(1)
print("Go package dependency boundaries: OK")
