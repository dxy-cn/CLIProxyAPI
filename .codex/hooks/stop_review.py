#!/usr/bin/env python3
import pathlib
import subprocess
import sys


ROOT = pathlib.Path(__file__).resolve().parents[2]


def run(args):
    return subprocess.run(args, cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)


def changed_files():
    names = set()
    for args in (
        ["git", "diff", "--name-only", "--cached"],
        ["git", "diff", "--name-only"],
        ["git", "ls-files", "--others", "--exclude-standard"],
    ):
        proc = run(args)
        if proc.returncode == 0:
            names.update(line.strip() for line in proc.stdout.splitlines() if line.strip())
    return sorted(names)


def main():
    files = changed_files()
    if not files:
        return

    risky = [
        path
        for path in files
        if path == ".env"
        or path.startswith(".env.")
        or path == "config.yaml"
        or path.startswith("auths/")
        or path.startswith("logs/")
        or path == "go.sum"
        or path.endswith((".pem", ".key", ".p12", ".crt", ".sqlite", ".db"))
    ]
    go_files = [path for path in files if path.endswith(".go")]

    messages = []
    if risky:
        messages.append("[codex] Review sensitive or dependency changes: " + ", ".join(risky))
    if go_files:
        messages.append("[codex] Backend Go files changed; expected checks: gofmt plus targeted go test/build.")

    if messages:
        print("\n".join(messages), file=sys.stderr)


if __name__ == "__main__":
    main()
