#!/usr/bin/env python3
"""Replace the private loopback demo, or restore its existing container."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Dict, List, Optional, Tuple


HOST_PORT = "18101"
CONTAINER_PORT = "8080/tcp"
LOOPBACK = "http://127.0.0.1:18101"
MEMORY_BYTES = 384 * 1024 * 1024
SWAP_BYTES = 768 * 1024 * 1024
CPU_NANO = 500_000_000
MANAGED_LABEL = "cc-agent-go.private-demo"


class DeploymentError(Exception):
    pass


def docker(*arguments: str, allow_failure: bool = False) -> Optional[str]:
    # Capture every Docker response: inspect output contains environment secrets.
    result = subprocess.run(
        ["sudo", "docker", *arguments],
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode:
        if allow_failure:
            return None
        raise DeploymentError(f"Docker {arguments[0]} failed (exit {result.returncode})")
    return result.stdout.strip()


def inspect_container(identifier: str) -> Optional[dict]:
    output = docker("inspect", "--type", "container", identifier, allow_failure=True)
    return json.loads(output)[0] if output is not None else None


def inspect_image(identifier: str) -> Optional[dict]:
    output = docker("inspect", "--type", "image", identifier, allow_failure=True)
    return json.loads(output)[0] if output is not None else None


def port_bindings(container: dict) -> dict:
    return container["HostConfig"].get("PortBindings") or {}


def find_live_container() -> dict:
    identifiers = (docker("ps", "--quiet") or "").splitlines()
    matches = []
    for identifier in identifiers:
        container = inspect_container(identifier)
        if container is None:
            continue
        for bindings in port_bindings(container).values():
            if any(binding.get("HostPort") == HOST_PORT for binding in bindings or []):
                matches.append(container)
                break
    if len(matches) != 1:
        raise DeploymentError("expected exactly one running container on demo port 18101")
    return matches[0]


def environment(container: dict) -> Dict[str, str]:
    entries = container["Config"].get("Env") or []
    values = {}
    for entry in entries:
        key, separator, value = entry.partition("=")
        if not separator or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            raise DeploymentError("current container has an unsupported environment entry")
        if key in values or "\n" in value or "\r" in value:
            raise DeploymentError("current container has an environment entry that cannot be copied")
        values[key] = value
    if len(values) != 6:
        raise DeploymentError("current demo environment count changed; review before deploying")
    return values


def validate_live(container: dict, replacement_image: dict) -> Dict[str, str]:
    config = container["Config"]
    host = container["HostConfig"]
    image_tag = config.get("Image", "")
    if not re.fullmatch(r"cc-agent-go:[0-9]+", image_tag):
        raise DeploymentError("demo port belongs to an unexpected image")
    tagged_image = inspect_image(image_tag)
    if tagged_image is None or tagged_image["Id"] != container["Image"]:
        raise DeploymentError("current image tag no longer identifies the running image")
    if port_bindings(container) != {
        CONTAINER_PORT: [{"HostIp": "127.0.0.1", "HostPort": HOST_PORT}]
    }:
        raise DeploymentError("demo port bindings changed; review before deploying")
    if host.get("Memory") != MEMORY_BYTES or host.get("MemorySwap") != SWAP_BYTES:
        raise DeploymentError("demo memory or swap limit changed; review before deploying")
    if (host.get("NanoCpus") != CPU_NANO
            or host.get("CpuQuota") not in (0, None)
            or host.get("CpuPeriod") not in (0, None)):
        raise DeploymentError("demo CPU limit changed; review before deploying")
    if host.get("RestartPolicy") != {"Name": "unless-stopped", "MaximumRetryCount": 0}:
        raise DeploymentError("demo restart policy changed; review before deploying")
    if container.get("Mounts") or host.get("Binds") or host.get("Mounts") or host.get("Tmpfs"):
        raise DeploymentError("demo now uses mounts; review before deploying")
    if host.get("Privileged") or host.get("AutoRemove") or host.get("Devices"):
        raise DeploymentError("demo container mode changed; review before deploying")
    if host.get("NetworkMode") not in ("default", "bridge"):
        raise DeploymentError("demo network mode changed; review before deploying")
    unsupported_limits = {
        "MemorySwappiness": (None, -1),
        "OomKillDisable": (None, False),
        "OomScoreAdj": (None, 0),
        "CpusetCpus": (None, ""),
        "CpusetMems": (None, ""),
    }
    for key, supported_values in unsupported_limits.items():
        if host.get(key) not in supported_values:
            raise DeploymentError(f"demo {key} changed; review before deploying")
    for key in ("User", "WorkingDir", "Entrypoint", "Cmd"):
        if config.get(key) != replacement_image["Config"].get(key):
            raise DeploymentError(f"replacement image changes {key}; review before deploying")
    return environment(container)


def docker_run_arguments(container: dict, image_tag: str, name: str, env_file: Path) -> List[str]:
    host = container["HostConfig"]
    arguments = [
        "run", "--detach", "--name", name,
        "--label", f"{MANAGED_LABEL}=true",
        "--restart", "unless-stopped",
        "--publish", f"127.0.0.1:{HOST_PORT}:{CONTAINER_PORT}",
        "--memory", str(host["Memory"]),
        "--memory-swap", str(host["MemorySwap"]),
        "--cpus", "0.5",
        "--env-file", str(env_file),
    ]
    optional_limits = {
        "MemoryReservation": "--memory-reservation",
        "PidsLimit": "--pids-limit",
        "CpuShares": "--cpu-shares",
        "ShmSize": "--shm-size",
    }
    for key, flag in optional_limits.items():
        value = host.get(key)
        if value not in (None, 0):
            arguments.extend((flag, str(value)))
    for capability in host.get("CapAdd") or []:
        arguments.extend(("--cap-add", capability))
    for capability in host.get("CapDrop") or []:
        arguments.extend(("--cap-drop", capability))
    for security_option in host.get("SecurityOpt") or []:
        arguments.extend(("--security-opt", security_option))
    for ulimit in host.get("Ulimits") or []:
        arguments.extend(("--ulimit", f"{ulimit['Name']}={ulimit['Soft']}:{ulimit['Hard']}"))
    if host.get("ReadonlyRootfs"):
        arguments.append("--read-only")
    if host.get("Init"):
        arguments.append("--init")
    log_config = host.get("LogConfig") or {}
    if log_config.get("Type"):
        arguments.extend(("--log-driver", log_config["Type"]))
    for key, value in (log_config.get("Config") or {}).items():
        arguments.extend(("--log-opt", f"{key}={value}"))
    arguments.append(image_tag)
    return arguments


def check_preserved_settings(previous: dict, current: dict) -> None:
    if environment(previous) != environment(current):
        raise DeploymentError("replacement environment differs from the live container")
    if port_bindings(previous) != port_bindings(current):
        raise DeploymentError("replacement port bindings differ from the live container")
    keys = (
        "Memory", "MemorySwap", "MemoryReservation", "NanoCpus", "CpuQuota",
        "CpuPeriod", "CpuShares", "PidsLimit", "ShmSize", "CapAdd", "CapDrop",
        "SecurityOpt", "Ulimits", "ReadonlyRootfs", "LogConfig", "RestartPolicy",
        "MemorySwappiness", "OomKillDisable", "OomScoreAdj", "CpusetCpus",
        "CpusetMems", "Init",
    )
    for key in keys:
        if previous["HostConfig"].get(key) != current["HostConfig"].get(key):
            raise DeploymentError(f"replacement container changed {key}")


def request(path: str, payload: Optional[bytes] = None) -> Tuple[int, bytes]:
    headers = {"Content-Type": "application/json"} if payload is not None else {}
    message = urllib.request.Request(LOOPBACK + path, data=payload, headers=headers)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(message, timeout=5) as response:
            return response.status, response.read(8192)
    except urllib.error.HTTPError as error:
        return error.code, error.read(8192)


def smoke() -> None:
    for route in ("/", "/harness", "/council", "/api/mcp/servers"):
        status, _ = request(route)
        if status != 200:
            raise DeploymentError(f"demo smoke failed at {route}: HTTP {status}")
    if request("/api/chat", b"{")[0] != 400:
        raise DeploymentError("demo chat malformed JSON check failed")
    status, body = request("/api/chat/stream", b"{")
    if status != 200 or b'"type":"error"' not in body:
        raise DeploymentError("demo streaming malformed JSON check failed")


def wait_for_smoke() -> None:
    for attempt in range(30):
        try:
            smoke()
            return
        except (DeploymentError, OSError):
            if attempt == 29:
                raise DeploymentError("replacement demo did not pass page and API smoke") from None
            time.sleep(2)


def write_environment_file(directory: Path, values: Dict[str, str]) -> Path:
    path = directory / "container.env"
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as output:
        for key, value in values.items():
            output.write(f"{key}={value}\n")
    return path


def restore(previous: dict, name: str, renamed: bool) -> None:
    replacement = inspect_container(name)
    if replacement is not None and replacement["Id"] != previous["Id"]:
        if replacement["Config"].get("Labels", {}).get(MANAGED_LABEL) != "true":
            raise DeploymentError("an unrecognized container holds the demo name")
        docker("rm", "--force", replacement["Id"])
    if renamed:
        docker("rename", previous["Id"], name)
    docker("start", previous["Id"])
    wait_for_smoke()


def deploy(build_number: str) -> None:
    image_tag = f"cc-agent-go:{build_number}"
    replacement_image = inspect_image(image_tag)
    if replacement_image is None:
        raise DeploymentError("replacement image is missing")
    previous = find_live_container()
    env_values = validate_live(previous, replacement_image)
    if previous["Config"]["Image"] == image_tag:
        raise DeploymentError("replacement image is already live")
    name = previous["Name"].lstrip("/")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", name):
        raise DeploymentError("current container name cannot be reused safely")
    backup_name = f"{name}-rollback-{build_number}"
    if inspect_container(backup_name) is not None:
        raise DeploymentError("a rollback container for this build already exists")
    smoke()  # Confirm the current service works before changing its container.
    with tempfile.TemporaryDirectory(prefix="cc-agent-go-deploy-") as temporary:
        env_file = write_environment_file(Path(temporary), env_values)
        stopped = False
        renamed = False
        try:
            docker("stop", previous["Id"])
            stopped = True
            docker("rename", previous["Id"], backup_name)
            renamed = True
            docker(*docker_run_arguments(previous, image_tag, name, env_file))
            current = inspect_container(name)
            if (current is None
                    or current["Config"].get("Image") != image_tag
                    or current["Image"] != replacement_image["Id"]):
                raise DeploymentError("replacement container identity does not match the build")
            check_preserved_settings(previous, current)
            wait_for_smoke()
        except (DeploymentError, OSError) as error:
            if not stopped:
                raise
            try:
                restore(previous, name, renamed)
            except (DeploymentError, OSError):
                raise DeploymentError(
                    "deployment failed and rollback could not be verified; inspect the server"
                ) from None
            raise DeploymentError(f"{error}; previous demo restored and smoke checked") from None
    print(f"Deployed {image_tag}; prior container retained as {backup_name}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--build-number", required=True)
    arguments = parser.parse_args()
    if not re.fullmatch(r"[1-9][0-9]*", arguments.build_number):
        parser.error("--build-number must be a positive decimal Jenkins build number")
    try:
        deploy(arguments.build_number)
    except (DeploymentError, OSError, ValueError, KeyError, TypeError) as error:
        print(f"Private demo deploy stopped: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
