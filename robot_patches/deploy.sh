#!/bin/bash
# Convenience shell wrapper for deploy.py
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
python3 "${SCRIPT_DIR}/deploy.py" "$@"
