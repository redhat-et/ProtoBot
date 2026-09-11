# Reference properties

- Select unwanted behavior with `If ... then` and preserve the timeout,
  caller, HTTP 503, and Retry-After header.
- Ask what Retry-After value or range is required and whether the response
  body or sensitive error disclosure is constrained.
- Suggest a recovery or retry companion only if grounded in the stated
  dependency failure.
- Do not invent a retry policy or identity-provider implementation.
