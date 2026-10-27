# 39. Semantic OKLCH design tokens and UI primitives

Status: Accepted

## Context

ADR 0033 derives the light theme by mirroring Tailwind's slate ramp
(50<->950). Any fixed colour (`text-white`, a -500 fill) meeting a flipped one
produced contrast bugs, and ~1,100 lines hard-code `slate-*`. Page frames and
buttons also differed page to page.

## Decision

- Declare ~19 semantic colour tokens (surface x3, fg x3, border, accent,
  on-accent, accent-fg, danger-solid, and success/warning/danger/info with
  `-soft` backgrounds) in plain `@theme` in `src/index.css` (dark values), and
  override them with explicit, hand-tuned values in a separate `.light` block
  that gen-light-theme never touches.
- `src/theme/tokens.test.ts` computes WCAG contrast from the OKLCH values for
  every intended text/surface pair in both themes (>= 4.5:1).
- Shared primitives live in `src/components/ui` (Button, Badge, Card, Field,
  PageFrame); see its README.
- Migration is incremental: Inbox, Task Detail and the sidebar first. The slate
  remap of ADR 0033 stays for unmigrated code until the rest moves.

## Consequences

- Migrated UI no longer depends on index mirroring; contrast is guarded by a test.
- Two colour systems coexist for a while; new code must use tokens.
- Approve is now a primary (accent) button; there is no green solid variant.
