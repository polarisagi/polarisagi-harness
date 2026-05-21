---
name: web_search
version: "1.0.0"
risk_level: medium
sandbox: L2
capability: write-network
---

# Web Search

Search the web and return structured results with optional page content.

## Precondition
- Query must be non-empty
- Domain allowlist applies

## Postcondition
- Ranked search results with title, URL, snippet, and optional full page content
