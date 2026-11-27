#!/usr/bin/env bash
# Shared helper for the bench scripts: resolve the OneDrive test account.
#
# The repository carries no account: the address is either provided by the
# caller (ACCOUNT=...) or typed interactively. There is deliberately no default.

# ocr_require_account <binary>
#   Prints the account address on stdout; returns non-zero when it cannot be
#   resolved. Prompts on stderr when stdin is a TTY, so stdout stays clean for
#   `ACCOUNT="$(ocr_require_account ...)"`.
ocr_require_account() {
    local bin="$1"

    if [ -n "${ACCOUNT:-}" ]; then
        printf '%s\n' "$ACCOUNT"
        return 0
    fi

    if [ -t 0 ]; then
        local accounts=() line
        if [ -x "$bin" ]; then
            while IFS= read -r line; do
                [[ "$line" =~ ^[[:space:]]*-[[:space:]]+(.+)$ ]] || continue
                accounts+=("${BASH_REMATCH[1]}")
            done < <("$bin" account list 2>/dev/null || true)
        fi

        local typed=""
        if [ "${#accounts[@]}" -gt 0 ]; then
            echo "Accounts configured on this machine (onecloudriver account list):" >&2
            local i=1 a
            for a in "${accounts[@]}"; do
                echo "  $i) $a" >&2
                i=$((i + 1))
            done
            read -r -p "Test account [1-${#accounts[@]} or address]: " typed || typed=""
            if [[ "$typed" =~ ^[0-9]+$ ]] && [ "$typed" -ge 1 ] && [ "$typed" -le "${#accounts[@]}" ]; then
                printf '%s\n' "${accounts[$((typed - 1))]}"
                return 0
            fi
        else
            read -r -p "Test account (address): " typed || typed=""
        fi
        if [ -n "$typed" ]; then
            printf '%s\n' "$typed"
            return 0
        fi
    fi

    cat >&2 <<'EOF'
ERROR: no test account given for the benchmark.
The repository does not ship any account; pass the address to measure:

    ACCOUNT=user@example.com ./run_fuse.sh compare

(`onecloudriver account list` shows the accounts configured on this machine)
EOF
    return 1
}
