# Reference properties

- Treat Redis, GraphQL, and the five-minute TTL as design ideas unless the
  user explicitly makes them externally visible constraints.
- Select event-driven for a search submission, but ask for latency, freshness,
  consistency, result ordering, and the meaning of newest.
- Flag `quickly` as vague and require an oracle before readiness.
- Keep the response implementation-independent.
