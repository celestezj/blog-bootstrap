#!/usr/bin/env sh
# Verifies that `blog-bootstrap agent-guide` matches docs/agent-install.md.
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

cd "$root"
go run ./cmd/blog-bootstrap agent-guide >"$tmp"
diff -u docs/agent-install.md "$tmp"
