# Reference properties

- Use the complex pattern because the concurrent-request state and GET event
  jointly scope the timing obligation.
- Preserve the exact endpoint, JSON response, UTF-8 body, timing boundaries,
  P100 meaning, inclusive 25-request boundary, and out-of-scope cases.
- Mark the primary candidate `ready for review` with both complete contracts.
- Do not invent auth, persistence, framework, or deployment behavior.
