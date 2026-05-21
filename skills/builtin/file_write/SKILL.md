---
name: file_write
version: "1.0.0"
risk_level: medium
sandbox: L2
capability: write-local
---

# File Write

Write content to a file on the local filesystem.

## Precondition
- File path must be within allowed directories
- Overwrite requires explicit confirmation

## Postcondition
- File created or updated with new content
