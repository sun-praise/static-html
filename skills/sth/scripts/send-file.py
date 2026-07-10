#!/usr/bin/env python3

import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

USAGE = """Usage: send-file.py <file.html> [options]

Options:
  --tag TAG          Tag(s) for the session (required)
  --category CAT     Category for the session (required)
  --project PROJ     Project for the session (required)
  --server URL       Server URL (default: $STATIC_HTML_SERVER_URL or http://127.0.0.1:3939)
  --api-key KEY      API key for authenticated servers (default: $STH_API_KEY)

Note: Only the specified HTML file is uploaded. If you need to include sibling
resources (CSS/JS/images), use 'sth send' directly with the appropriate directory.
"""


def die(msg, code=1):
    print(msg, file=sys.stderr)
    sys.exit(code)


def usage(code=1):
    print(USAGE, file=sys.stderr, end="")
    sys.exit(code)


def trim(value):
    """Normalize an option value: strip surrounding whitespace and collapse newlines to spaces.

    Only formats the value; emptiness is NOT validated here and must be checked by the caller.
    """
    value = value.strip()
    value = value.replace("\n", " ")
    return value


def parse_args(argv):
    target_file = None
    server_url = os.environ.get("STATIC_HTML_SERVER_URL", "http://127.0.0.1:3939")
    tag = ""
    category = ""
    project = ""
    api_key = os.environ.get("STH_API_KEY", "")

    if not argv:
        usage(1)
    if argv[0] in ("--help", "-h"):
        usage(0)

    def need_value(name, val):
        if val is None:
            print(f"Error: {name} requires a value", file=sys.stderr)
            usage(1)
        return trim(val)

    i = 0
    while i < len(argv):
        arg = argv[i]

        if arg in ("--help", "-h"):
            usage(0)
        elif arg == "--tag":
            tag = need_value("--tag", argv[i + 1] if i + 1 < len(argv) else None)
            i += 2
        elif arg.startswith("--tag="):
            tag = trim(arg[len("--tag="):])
            if not tag:
                print("Error: --tag requires a value", file=sys.stderr)
                usage(1)
            i += 1
        elif arg == "--category":
            category = need_value("--category", argv[i + 1] if i + 1 < len(argv) else None)
            i += 2
        elif arg.startswith("--category="):
            category = trim(arg[len("--category="):])
            if not category:
                print("Error: --category requires a value", file=sys.stderr)
                usage(1)
            i += 1
        elif arg == "--project":
            project = need_value("--project", argv[i + 1] if i + 1 < len(argv) else None)
            i += 2
        elif arg.startswith("--project="):
            project = trim(arg[len("--project="):])
            if not project:
                print("Error: --project requires a value", file=sys.stderr)
                usage(1)
            i += 1
        elif arg == "--server":
            server_url = need_value("--server", argv[i + 1] if i + 1 < len(argv) else None)
            i += 2
        elif arg.startswith("--server="):
            server_url = trim(arg[len("--server="):])
            if not server_url:
                print("Error: --server requires a value", file=sys.stderr)
                usage(1)
            i += 1
        elif arg == "--api-key":
            api_key = need_value("--api-key", argv[i + 1] if i + 1 < len(argv) else None)
            i += 2
        elif arg.startswith("--api-key="):
            api_key = trim(arg[len("--api-key="):])
            i += 1
        elif arg == "--":
            i += 1
            break
        elif arg.startswith("-"):
            print(f"Unknown option: {arg}", file=sys.stderr)
            usage(1)
        else:
            if target_file is None:
                target_file = arg
                i += 1
            else:
                print(f"Unexpected argument: {arg}", file=sys.stderr)
                usage(1)

    while i < len(argv):
        arg = argv[i]
        if target_file is None:
            target_file = arg
        else:
            print(f"Unexpected argument: {arg}", file=sys.stderr)
            usage(1)
        i += 1

    if target_file is None:
        print("Error: <file.html> is required", file=sys.stderr)
        usage(1)
    if not re.match(r"^https?://", server_url):
        die("Error: server URL must start with http:// or https://")
    if not tag:
        print("Error: --tag is required", file=sys.stderr)
        usage(1)
    if not category:
        print("Error: --category is required", file=sys.stderr)
        usage(1)
    if not project:
        print("Error: --project is required", file=sys.stderr)
        usage(1)

    return target_file, server_url, tag, category, project, api_key


def resolve_target(target_file):
    original = target_file
    try:
        resolved = Path(target_file).resolve(strict=True)
    except FileNotFoundError:
        die(f"Error: file not found or inaccessible: {original}")
    except OSError as exc:
        die(f"Error: cannot resolve path: {original}: {exc}")

    if not resolved.is_file():
        die(f"Error: file not found: {resolved}")

    filename = resolved.name
    lowername = filename.lower()
    if not (lowername.endswith(".html") or lowername.endswith(".htm")):
        die(f"Error: file must have .html or .htm extension: {filename}")

    return resolved, filename


def bootstrap_repo(script_dir):
    bootstrap = script_dir / "bootstrap-repo.sh"
    try:
        result = subprocess.run(
            ["bash", str(bootstrap)],
            check=True,
            capture_output=True,
            text=True,
        )
    except subprocess.CalledProcessError as exc:
        sys.stderr.write(exc.stderr or "")
        die(f"Error: bootstrap-repo.sh failed with exit code {exc.returncode}")

    repo_dir = Path(result.stdout.strip())
    if not repo_dir.is_dir():
        die(f"Error: bootstrap-repo.sh did not return a valid directory: {repo_dir}")
    return repo_dir


def main():
    target_file, server_url, tag, category, project, api_key = parse_args(sys.argv[1:])
    resolved, filename = resolve_target(target_file)

    script_dir = Path(__file__).resolve().parent
    repo_dir = bootstrap_repo(script_dir)
    sth_bin = repo_dir / "dist" / "sth"
    if not sth_bin.is_file():
        die(f"Error: sth binary not found: {sth_bin}. Did bootstrap-repo.sh build successfully?")

    with tempfile.TemporaryDirectory(prefix="send-file-") as tmpdir:
        tmp_path = Path(tmpdir)
        os.chmod(tmp_path, 0o700)
        shutil.copy2(resolved, tmp_path / filename)

        cmd = [
            str(sth_bin),
            "send",
            str(tmp_path / filename),
            "--server",
            server_url,
            "--tag",
            tag,
            "--category",
            category,
            "--project",
            project,
        ]
        # Pass the API key via the child environment (STH_API_KEY) rather than
        # as an argv element, so it is not visible in `ps`/process listings.
        child_env = None
        if api_key:
            child_env = dict(os.environ)
            child_env["STH_API_KEY"] = api_key

        completed = subprocess.run(cmd, cwd=str(repo_dir), env=child_env)
        sys.exit(completed.returncode)


if __name__ == "__main__":
    main()
