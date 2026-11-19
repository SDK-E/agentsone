<!-- SPDX-License-Identifier: MIT -->

# Type

Agents One uses two open fonts from the Atkinson Hyperlegible family, made by the Braille Institute for readers with low vision. Every letter is drawn to be told apart from its neighbours: capital I, lower-case l and the digit 1 never look alike. That suits a system where people read agent names, numbers and logs at a glance.

| Role | Family | Files |
| --- | --- | --- |
| Interface | Atkinson Hyperlegible Next | [`atkinson-hyperlegible-next/`](atkinson-hyperlegible-next/) |
| Code, logs, ids, numbers in tables | Atkinson Hyperlegible Mono | [`atkinson-hyperlegible-mono/`](atkinson-hyperlegible-mono/) |

Both are variable fonts (weight 200 to 800) with upright and italic styles, in WOFF2, split into Latin and Latin Extended subsets. They are bundled because the machine has no internet at first boot. Load them with [`fonts.css`](fonts.css).

## Licence

Both families are under the SIL Open Font License 1.1. The full text sits beside the files as `OFL.txt`. You may use, bundle and redistribute them. You may not sell the fonts on their own. The WOFF2 files come unchanged from the Fontsource packages `@fontsource-variable/atkinson-hyperlegible-next` and `@fontsource-variable/atkinson-hyperlegible-mono`, version 5.3.0.

## Scale

Base size is 16 px. Line height is 1.5 for body text and 1.25 for headings.

| Token | Size | Weight | Use |
| --- | --- | --- | --- |
| `display` | 32 px | 700 | One per screen at most |
| `title` | 24 px | 700 | Page titles |
| `heading` | 20 px | 600 | Panel titles |
| `body` | 16 px | 400 | Everything else |
| `label` | 14 px | 500 | Labels, captions, the live list |
| `mono` | 14 px | 400 | Code, logs, ids |

Nothing in the interface is smaller than 14 px.

## Stacks

```css
font-family: "Atkinson Hyperlegible Next", system-ui, sans-serif;
font-family: "Atkinson Hyperlegible Mono", ui-monospace, monospace;
```

## Do and don't

- Use weight to show order: 400 for reading, 500 to 600 for labels and headings, 700 for titles.
- Use tabular figures (`font-variant-numeric: tabular-nums`) for numbers that change, such as budgets and counts.
- Do not set long text in mono. Mono is for things a person might copy.
- Do not use letter spacing on body text.
