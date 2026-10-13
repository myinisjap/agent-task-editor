# 33. Light theme is derived by remapping Tailwind CSS variables

Status: Accepted

## Context

The UI was authored dark-first with Tailwind's `slate` palette. Adding `dark:`
variants to every component would be a huge diff and keep drifting.

## Decision

Keep the dark palette as-is and derive the light theme by remapping
`--color-<name>-<shade>` variables under a `.light` root class in
`src/index.css`; Tailwind v4 compiles utilities to `var(--color-...)`, so no
component edits are needed. Light values are an index-mirror of the dark ramp,
regenerated with `node scripts/gen-light-theme.mjs` after a Tailwind upgrade.
`stores/theme.ts` persists the choice (default from `prefers-color-scheme`) and
an inline script in `index.html` applies the class before first paint.

## Consequences

- Prefer Tailwind `slate`/accent utilities over hardcoded hex so new UI themes
  automatically.
