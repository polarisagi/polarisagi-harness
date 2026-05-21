---
name: api_call
version: "1.0.0"
risk_level: high
sandbox: L2
capability: write-network
---

# API Call

Make an HTTP API call to an external service.

## Precondition
- URL must be in allowed_endpoints list
- Authentication from capability token

## Postcondition
- Response body, headers, status code returned
