---
name: file_search
version: "1.0.0"
risk_level: low
sandbox: L1
capability: read-only
---

# File Search

Search file contents recursively using pattern matching.

## Precondition
- Root path must be within allowed directories

## Postcondition
- List of matched files with line numbers returned
