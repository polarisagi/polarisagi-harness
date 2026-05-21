---
name: json_parse
version: "1.0.0"
risk_level: low
sandbox: L1
capability: read-only
---

# JSON Parse

Parse a JSON string into a structured Go value. Validates schema compliance.

## Precondition
- Input must be valid JSON

## Postcondition
- Parsed structured data returned
- Validation errors returned if schema validation fails
