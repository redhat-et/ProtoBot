#!/usr/bin/env bash
set -euo pipefail
exec python3 "$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/scripts/pre_commit_cache.py" --upstream code
