---
name: data_query
version: "1.0.0"
risk_level: low
sandbox: L1
capability: read-only
---

# Data Query

Query structured data sources (SQLite) using SQL.

## Precondition
- Query must be SELECT-only (read access)
- Database must be in allowed sources

## Postcondition
- Query results returned as structured rows
