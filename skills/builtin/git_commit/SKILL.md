---
name: git_commit
version: "1.0.0"
risk_level: high
sandbox: L2
capability: write-local
---

# Git Commit

Stage and commit changes to a git repository.

## Precondition
- Working directory must be a git repository
- Commit message must be non-empty

## Postcondition
- Changes staged and committed
- Commit hash returned
