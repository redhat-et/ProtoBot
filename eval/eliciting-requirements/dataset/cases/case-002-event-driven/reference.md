# Reference properties

- Select the event-driven pattern because submission is a discrete boundary
  event.
- Preserve `valid payment`, `authorization result`, and `2 seconds`.
- Ask what starts and ends the timing measurement and what result states are
  observable if those definitions are not supplied.
- Do not turn the request into a payment-provider, HTTP, or queue design.
