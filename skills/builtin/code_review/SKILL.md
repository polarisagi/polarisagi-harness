---
name: code_review
version: "1.0.0"
risk_level: low
sandbox: L1
capability: read-only
---

# Code Review

Analyze a code diff and produce structured review feedback.

## Precondition
- Code content or diff must be provided

## Postcondition
- Review report with severity, category, suggestion, and line reference
