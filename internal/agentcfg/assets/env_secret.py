#!/usr/bin/env python3
"""Read a named env secret for email.sh, without invoking a shell.

Based on jugui's env_secret.py. Decode lolad's escaped double-quoted values,
optional exports and trailing comments; single quotes preserve literal text.
"""
import ast
import re
import sys


def read_value(raw):
    value = raw.strip()
    if not value or value[0] not in "\"'":
        return value.partition("#")[0].strip()
    quote = value[0]
    escaped = False
    for i in range(1, len(value)):
        if value[i] == quote and (quote == "'" or not escaped):
            literal = value[:i + 1]
            return ast.literal_eval(literal) if quote == '"' else literal[1:-1]
        if value[i] == "\\":
            escaped = not escaped
        else:
            escaped = False
    raise ValueError("unterminated quoted value")


def main():
    if len(sys.argv) != 3:
        print("Usage: env_secret.py /path/to/.env NAME", file=sys.stderr)
        return 2
    path, want = sys.argv[1:]
    found = None
    try:
        with open(path, encoding="utf-8") as source:
            for raw in source:
                line = raw.strip()
                if line.startswith("#"):
                    continue
                line = re.sub(r"^export\s+", "", line)
                key, sep, value = line.partition("=")
                if sep and key.strip() == want:
                    found = read_value(value)
    except (OSError, ValueError, SyntaxError):
        print("Error: cannot read env secret", file=sys.stderr)
        return 1
    if found is None:
        print("Error: env secret not found", file=sys.stderr)
        return 1
    sys.stdout.write(found)
    return 0


if __name__ == "__main__":
    sys.exit(main())
