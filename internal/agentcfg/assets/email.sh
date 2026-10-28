#!/bin/bash
# Lola Email Client - Main wrapper
#
# Usage:
#   email.sh check alex@example.com
#   email.sh read alex@example.com 12345
#   email.sh send assistant@example.com --to "user@example.com" --subject "Hello" --body "Message"
#   email.sh reply alex@example.com 12345 --body "My reply"
#   email.sh forward alex@example.com 12345 --to "user@example.com"
#   email.sh search "invoice" --account alex@example.com
#   email.sh sync alex@example.com --days 7
#   email.sh accounts
#   email.sh watch alex@example.com      # JSON of inbox mail since the last watch
#   email.sh secrets-needed              # names (never values) of the secrets the accounts use
#
# Based on lolabot's tools/email.sh (MIT License, Copyright (c) 2026 23blocks Inc.);
# installed and kept up to date by lolad. lolad also runs this script itself (for
# `watch`) with a minimal environment, so it does not rely on the agent's shell setup.

LOLABOT_HOME="${LOLABOT_HOME:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VENV_DIR="$LOLABOT_HOME/.venv"

# A daemon's PATH may be only /usr/bin:/bin. Look in the usual install places too,
# after whatever PATH already has.
for D in /opt/homebrew/bin /usr/local/bin "${HOME:+$HOME/.local/bin}" "${HOME:+$HOME/.cargo/bin}"; do
    if [ -n "$D" ] && [ -d "$D" ]; then
        case ":$PATH:" in *":$D:"*) ;; *) PATH="$PATH:$D" ;; esac
    fi
done
export PATH

# The interpreter: the agent's .venv when it has one, else python3.
if [ -x "$VENV_DIR/bin/python" ]; then
    PY="$VENV_DIR/bin/python"
else
    PY="$(command -v python3 || true)"
fi

has_yaml() { [ -n "$1" ] && "$1" -c 'import yaml' >/dev/null 2>&1; }

# The mail client needs PyYAML. Install it into .venv if the interpreter lacks it.
# Progress goes to stderr: stdout is the command's output (lolad parses `watch`).
if ! has_yaml "$PY"; then
    UV="$(command -v uv || true)"
    echo "email.sh: PyYAML is missing; installing it into $VENV_DIR" >&2
    if [ ! -e "$VENV_DIR" ]; then
        BASE="$(command -v python3 || true)"
        if [ -n "$UV" ]; then
            if [ -n "$BASE" ]; then
                "$UV" venv --python "$BASE" "$VENV_DIR" >&2
            else
                "$UV" venv "$VENV_DIR" >&2
            fi
        elif [ -n "$BASE" ]; then
            "$BASE" -m venv "$VENV_DIR" >&2
        fi
    fi
    if [ ! -x "$VENV_DIR/bin/python" ]; then
        echo "Error: no usable Python environment at $VENV_DIR (and none could be created)." >&2
        echo "       Install python3 (or uv), then run: python3 -m venv \"$VENV_DIR\"" >&2
        exit 1
    fi
    PY="$VENV_DIR/bin/python"
    OK=0
    if [ -n "$UV" ] && "$UV" pip install --python "$PY" pyyaml >&2; then
        OK=1
    else
        # A venv made by uv has no pip; ensurepip adds it.
        if ! "$PY" -m pip --version >/dev/null 2>&1; then
            "$PY" -m ensurepip --upgrade >&2
        fi
        if "$PY" -m pip install pyyaml >&2; then
            OK=1
        fi
    fi
    if [ "$OK" != 1 ] || ! has_yaml "$PY"; then
        echo "Error: could not install PyYAML into $VENV_DIR, which the mail client needs." >&2
        echo "       Install it by hand: uv pip install --python \"$PY\" pyyaml" >&2
        echo "       (or: \"$PY\" -m pip install pyyaml)" >&2
        exit 1
    fi
    echo "email.sh: PyYAML installed." >&2
fi

CLIENT="$SCRIPT_DIR/email_client.py"

# Accounts may name their password (password_secret: NAME in the credentials file).
# The values come from the environment, the agent's env file, or aim-secret; never printed.
USE_ARGS=()
if [ "${1:-}" != "secrets-needed" ]; then
    if ! NEEDED="$("$PY" "$CLIENT" secrets-needed)"; then
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

if [ ${#USE_ARGS[@]} -eq 0 ]; then
    exec "$PY" "$CLIENT" "$@"
fi

# lolad exports the agent's env file before launch: reuse values already exported.
MISSING=()
for ((i = 1; i < ${#USE_ARGS[@]}; i += 2)); do
    NAME="${USE_ARGS[$i]}"
    if [ -z "${!NAME:-}" ]; then
        MISSING+=("$NAME")
    fi
done
if [ ${#MISSING[@]} -eq 0 ]; then
    exec "$PY" "$CLIENT" "$@"
fi

# Otherwise read them from the agent's env file: LOLAD_ENV_FILE, else lolabot.yaml
# runtime.env_file (relative to the agent dir), else .env.
ENV_FILE="${LOLAD_ENV_FILE:-}"
if [ -z "$ENV_FILE" ] && [ -f "$LOLABOT_HOME/lolabot.yaml" ]; then
    READ_ENV_FILE='
import sys, yaml
try:
    with open(sys.argv[1], encoding="utf-8") as f:
        cfg = yaml.safe_load(f) or {}
    v = (cfg.get("runtime") or {}).get("env_file") or ""
    if isinstance(v, str) and "\n" not in v:
        print(v)
except Exception:
    pass
'
    ENV_FILE="$("$PY" -c "$READ_ENV_FILE" "$LOLABOT_HOME/lolabot.yaml" 2>/dev/null)"
fi
ENV_FILE="${ENV_FILE:-.env}"
case "$ENV_FILE" in
    /*) ;;
    *) ENV_FILE="$LOLABOT_HOME/$ENV_FILE" ;;
esac

if [ -f "$ENV_FILE" ]; then
    # Take the named secrets from the env file (owner-only, git-ignored). Preferred over
    # aim-secret, which belongs to AI Maestro's vault and may not be this agent's to use.
    # A name missing here is left unset; the client says which one when an account needs it.
    for NAME in "${MISSING[@]}"; do
        if VALUE="$("$PY" "$SCRIPT_DIR/env_secret.py" "$ENV_FILE" "$NAME" 2>/dev/null)"; then
            export "$NAME=$VALUE"
        fi
    done
    unset VALUE
    exec "$PY" "$CLIENT" "$@"
fi

if command -v aim-secret >/dev/null 2>&1; then
    exec aim-secret exec "${USE_ARGS[@]}" -- "$PY" "$CLIENT" "$@"
fi

echo "Error: the accounts' passwords are not set: no env file at $ENV_FILE." >&2
echo "       Set them in the agent's email settings in lolad (they are stored in .env)." >&2
exit 3
