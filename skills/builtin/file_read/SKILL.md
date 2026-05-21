---
name: file_read
version: "1.0.0"
risk_level: low
sandbox: L1
capability: read-only
---

# File Read

Read contents from a file on the local filesystem.

## Precondition
- File path must be within allowed directories
- File must exist and be readable

## Postcondition
- File content returned as string or bytes
