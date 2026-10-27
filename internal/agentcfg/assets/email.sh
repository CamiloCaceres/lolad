#!/bin/bash
# Lola Email Client - Main wrapper
#
# Usage:
#   email.sh check alex@example.com
#   email.sh read alex@example.com 12345
#   email.sh send assistant@example.com --to "user@example.com" --subject "Hello" --body "Message"
#   email.sh reply alex@example.com 12345 --body "My reply"
#   email.sh search "invoice" --account alex@example.com
#   email.sh sync alex@example.com --days 7
#   email.sh accounts

LOLABOT_HOME="${LOLABOT_HOME:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VENV_DIR="$LOLABOT_HOME/.venv"

# Activate venv if exists
if [ -d "$VENV_DIR" ]; then
    source "$VENV_DIR/bin/activate"
fi

# Accounts may keep their password in the vault (password_secret: NAME in the credentials file).
# Then the mail client runs under `aim-secret exec`, which puts the value in its environment and
# scrubs it from the output, so the agent never sees it.
USE_ARGS=()
if [ "${1:-}" != "secrets-needed" ]; then
    if ! NEEDED="$(python3 "$SCRIPT_DIR/email_client.py" secrets-needed)"; then
        exit 1   # the reason was printed on stderr
    fi
    while IFS= read -r NAME; do
        [ -z "$NAME" ] && continue
        if [[ ! "$NAME" =~ ^[A-Z][A-Z0-9_]{0,63}$ ]]; then
            echo "Error: refusing unexpected secret name from the mail client" >&2
            exit 1
        fi
        USE_ARGS+=(--use "$NAME")
    done <<< "$NEEDED"
fi

# lolad exports the selected runtime env_file before launch. Reuse those named
# values, including when runtime.env_file points somewhere other than .env.
if [ ${#USE_ARGS[@]} -gt 0 ]; then
    ALL_SECRETS_EXPORTED=1
    for ((i = 1; i < ${#USE_ARGS[@]}; i += 2)); do
        NAME="${USE_ARGS[$i]}"
        if [ -z "${!NAME:-}" ]; then
            ALL_SECRETS_EXPORTED=0
        fi
    done
    if [ "$ALL_SECRETS_EXPORTED" = 1 ]; then
        exec python3 "$SCRIPT_DIR/email_client.py" "$@"
    fi
fi

ENV_FILE="${LOLAD_ENV_FILE:-$LOLABOT_HOME/.env}"
if [ ${#USE_ARGS[@]} -gt 0 ] && [ -f "$ENV_FILE" ]; then
    # Take the named secrets from .env (owner-only, git-ignored). Preferred over aim-secret, which
    # belongs to AI Maestro's vault and may not be this agent's to use.
    for ((i = 1; i < ${#USE_ARGS[@]}; i += 2)); do
        NAME="${USE_ARGS[$i]}"
        VALUE="$(python3 "$SCRIPT_DIR/env_secret.py" "$ENV_FILE" "$NAME")" || exit 3
        export "$NAME=$VALUE"
    done
    unset VALUE
    exec python3 "$SCRIPT_DIR/email_client.py" "$@"
fi

if [ ${#USE_ARGS[@]} -gt 0 ]; then
    if ! command -v aim-secret >/dev/null 2>&1; then
        echo "Error: this account keeps its password in the vault, but aim-secret is not installed." >&2
        echo "       Install AI Maestro's aim-secret, then run: aim-secret set <NAME>" >&2
        exit 3
    fi
    exec aim-secret exec "${USE_ARGS[@]}" -- python3 "$SCRIPT_DIR/email_client.py" "$@"
fi

exec python3 "$SCRIPT_DIR/email_client.py" "$@"
