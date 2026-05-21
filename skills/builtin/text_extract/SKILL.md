---
name: text_extract
version: "1.0.0"
risk_level: low
sandbox: L1
capability: read-only
---

# Text Extract

Extract structured data (entities, key-value pairs, tables) from unstructured text.

## Precondition
- Input text must be non-empty
- Extraction schema must be specified

## Postcondition
- Structured data returned matching the specified schema
