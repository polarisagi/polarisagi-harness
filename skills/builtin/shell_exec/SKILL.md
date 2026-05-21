---
name: shell_exec
version: "1.0.0"
risk_level: high
sandbox: L2
capability: write-local
---

# Shell Exec

Execute a shell command in a Wasm-sandboxed environment.

## Precondition
- Command must be in allowed_commands list
- No network access unless explicitly granted

## Postcondition
- stdout/stderr returned
- Exit code recorded
