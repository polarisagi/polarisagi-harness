---
name: web_fetch
version: "1.0.0"
risk_level: medium
sandbox: L2
capability: write-network
---

# Web Fetch

Fetch content from a URL and return as structured data.

## Precondition
- URL must pass domain allowlist check
- Response size bounded by max_bytes

## Postcondition
- Response body, headers, and status code returned
