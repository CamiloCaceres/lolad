#!/bin/bash
# Quick email send
#
# Usage:
#   email-send.sh --to "user@example.com" --subject "Hello" --body "Message"
#   email-send.sh --from assistant@example.com --to "user@example.com" --subject "Hello" --body "Message"
#
# Based on lolabot's tools/email-send.sh (MIT License, Copyright (c) 2026 23blocks Inc.);
# installed by lolad with its email tools.

LOLABOT_HOME="${LOLABOT_HOME:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

ACCOUNT=""
ARGS=()
while [[ $# -gt 0 ]]; do
    case $1 in
        --from)
            ACCOUNT="$2"
            shift 2
            ;;
        *)
            ARGS+=("$1")
            shift
            ;;
    esac
done

# Default sender: the first account in lolabot.yaml (email.accounts).
if [ -z "$ACCOUNT" ]; then
    # email.sh makes sure the interpreter has PyYAML; it prints secret names only.
    "$SCRIPT_DIR/email.sh" secrets-needed >/dev/null || exit 1
    PY="$LOLABOT_HOME/.venv/bin/python"
    [ -x "$PY" ] || PY="$(command -v python3 || echo python3)"
    ACCOUNT="$(LOLABOT_HOME="$LOLABOT_HOME" "$PY" -c '
import sys
sys.path.insert(0, sys.argv[1])
from config import load_config
accts = load_config().get("email", {}).get("accounts") or []
print(accts[0].get("address", "") if accts and isinstance(accts[0], dict) else "")
' "$SCRIPT_DIR" 2>/dev/null)"
fi

if [ -z "$ACCOUNT" ]; then
    echo "Error: no sender. Pass --from, or list an account under email.accounts in lolabot.yaml." >&2
    exit 1
fi

exec "$SCRIPT_DIR/email.sh" send "$ACCOUNT" "${ARGS[@]}"
