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

- Contrast: `white`/`black` are not remapped and shades 500/600 are stable while
  50–400 and 700–900 flip. Use `text-slate-50` on slate fills and keep
  `text-white` for -500/-600 accent fills only (enforced by an axe e2e spec and
  a static vitest guard).
- Prefer Tailwind `slate`/accent utilities over hardcoded hex so new UI themes
  automatically.
