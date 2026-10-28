# UI primitives and design tokens

Tokens are OKLCH colours declared in `@theme` in `src/index.css` (dark values,
the default). Light values are overridden in a hand-written `.light` block
(separate from the generated slate remap). Tailwind exposes each as a utility:
`bg-surface`, `text-fg-muted`, `border-border`, `ring-accent`, `bg-accent/90`…
Contrast (>= 4.5:1) is enforced by `src/theme/tokens.test.ts`; edit values the CSS and keep the test passing.

| Token | Purpose | Dark | Light | AA on |
|---|---|---|---|---|
| `surface` | page background | 0.129 0.042 264.7 | 0.984 0.003 247.9 | — |
| `surface-raised` | cards, sidebar, panels, action bars | 0.208 0.042 265.8 | 1 0 0 | — |
| `surface-sunken` | inputs, wells, hover/active nav | 0.279 0.041 260 | 0.955 0.008 250 | — |
| `fg` | primary text | 0.929 0.013 255.5 | 0.21 0.04 266 | all surfaces |
| `fg-muted` | secondary text | 0.75 0.035 257 | 0.45 0.04 257 | all surfaces |
| `fg-subtle` | tertiary text, placeholders | 0.68 0.035 257 | 0.52 0.035 257 | all surfaces |
| `border` | borders, dividers | 0.372 0.044 257.3 | 0.869 0.022 252.9 | — |
| `accent` | primary fill, focus/selected ring | 0.51 0.23 277 | 0.51 0.23 277 | `on-accent` text |
| `on-accent` | text on `accent` / `danger-solid` | 1 0 0 | 1 0 0 | accent fills |
| `accent-fg` | accent-coloured text, links, active tab | 0.79 0.11 275 | 0.46 0.2 277 | all surfaces |
| `danger-solid` | danger button fill | 0.51 0.19 27 | 0.51 0.19 27 | `on-accent` text |
| `success` / `-soft` | status text / tinted bg | see css | see css | all surfaces + own `-soft` |
| `warning` / `-soft` | idem | | | |
| `danger` / `-soft` | idem | | | |
| `info` / `-soft` | idem | | | |
| `gate` / `-soft` | idem (human-gate highlight: pink) | | | |

**Fill vs text rule.** Status tokens are *text/icon* colours for use on surfaces
and their own `-soft` background. Only `accent` and `danger-solid` are fills,
and they take `text-on-accent`. Never use `text-white`.

## Primitives (`import { … } from '../ui'`)

- `<Button variant="primary|secondary|ghost|danger" size="sm|md" loading>` — defaults to `type="button"` (pass `type="submit"` in forms). `loading` disables, sets `aria-busy` and shows a spinner. `buttonClasses({variant,size,className})` styles a `<Link>` as a button.
- `<Badge tone="neutral|accent|success|warning|danger|info" variant="soft|solid" size="sm|xs">` — solid exists only for accent and danger; other tones fall back to soft.
- `<Card as padding="none|sm|md" selected interactive>` — rest props (onClick, data-testid, aria-*) pass through.
- `<Field label hint error labelHidden>` wraps one input/select/textarea (use `controlClasses`) and wires id, `aria-describedby` and `aria-invalid`.
- `<PageFrame title titleAdornment help actions tabs description fill headerClassName width="default|wide|full" testId>` — one page width and header. The h1 contains only the title text. `description` is a muted line under the header; `fill` makes the root a full-height flex column (children in a `flex-1 min-h-0` flex column) for boards/graphs; `headerClassName` overrides the header margin (default `mb-6`).
- `<Icon icon={Pause} size="xs|sm|md|lg">` — Lucide icon (named import from `lucide-react`; never `import *`). xs = 10px badges, sm = text-xs buttons, md = nav and icon-only buttons, lg = bottom tab bar. Icons are decorative (`aria-hidden`): the text label, or an `aria-label` on icon-only buttons, carries the meaning. No emoji as UI icons.

```tsx
<PageFrame title="Inbox" actions={<Button variant="primary">New</Button>}>
  <Card padding="md"><Badge tone="success">Verified</Badge></Card>
</PageFrame>
```

## Migration guidance

slate-950/900/800 → `surface`/`-raised`/`-sunken`; slate-200 → `fg`; slate-400 → `fg-muted`; slate-500 → `fg-subtle`; slate-700 border → `border`; indigo/sky primary buttons → `Button primary`; emerald/red-900 pills → `Badge` tones. Pages with no token for a hue map to the closest tone.

**Page width / header rule.** `width="default"` for every content page (Insights, Repos, all Settings tabs, Pipeline → Agents); `width="full"` + `fill` only for the Board and Pipeline → Overview / Workflow graphs. Header pattern: `title` + `help={{ onClick, title }}` + `actions` (Buttons) + optional `description`. Settings and Pipeline tabs render their own PageFrame; TabbedPage only draws the tab strip.

Task Detail and Chat do **not** use PageFrame: they are full-height split layouts (Task Detail's header is TaskHeader; Chat is a terminal split with no page h1). Don't force it.

`scripts/gen-light-theme.mjs` and the generated `.light { --color-slate-* }` block stay for unmigrated classes; never edit the semantic `.light` block by running the generator.
