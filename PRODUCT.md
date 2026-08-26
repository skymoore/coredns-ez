# Product

## Register

product

## Users

CoreDNS operators running this out-of-tree admin plugin. They manage zones and records on a primary, inspect secondaries, and handle local users, API tokens, and cluster membership from an operator console on the DoH listener.

## Product Purpose

The admin UI is the management plane multiplexed onto CoreDNS’s HTTPS listener. `/dns-query` stays DoH. Success is an operator who can inspect this node, edit zone data, and trust identity without leaving the DNS process.

## Brand Personality

Official CoreDNS: precise, operator-grade, purple-on-paper. Voice is short and literal. Personality: exact, calm, infrastructural.

## Anti-references

Default shadcn zinc dashboards, Inter as brand type, AI-purple glow, Lucide mixed with Phosphor, JWT in localStorage, three equal hero metric cards, cream/sand canvases, em-dashes in copy.

## Design Principles

- The tool disappears into the task: familiar DNS admin affordances (BIND-style `@` and `$ORIGIN`), not invented chrome.
- Practice what coredns.io preaches: vendored Lato, official purple, no second brand system.
- Show live node state; do not decorate metrics.
- Deny-by-default: every control has an obvious role gate and a failure state.
- Density over flourish: tables, forms, and serials earn the pixels.

## Accessibility & Inclusion

WCAG 2.2 AA for text contrast. Keyboard-complete forms and dialogs. Honor `prefers-reduced-motion`. Do not convey record state by color alone.
