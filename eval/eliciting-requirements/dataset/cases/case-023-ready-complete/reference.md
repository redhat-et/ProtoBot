# Reference properties

- Select complex because the concurrency state and request event jointly scope
  the timing obligation.
- Preserve the exact endpoint, actor, status, content type, JSON body, timing
  boundaries, 50-request load, and explicit out-of-scope cases.
- Mark the complete primary candidate `ready for review` with both contracts.
- Do not invent auth, persistence, framework, or deployment behavior.
