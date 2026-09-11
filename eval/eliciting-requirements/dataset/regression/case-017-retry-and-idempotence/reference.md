# Reference properties

- Split the save behavior from the network-failure behavior.
- Use event-driven for a save request and unwanted behavior for network
  failure, with complex form only if a shared condition is genuinely needed.
- Ask for retry limit or terminal policy, timing/backoff, cancellation,
  duplicate effects, partial completion, and observable failure evidence.
- Flag `as needed` as vague and do not invent infinite retry behavior.
