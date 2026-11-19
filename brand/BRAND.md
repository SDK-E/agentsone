<!-- SPDX-License-Identifier: MIT -->

# Agents One brand

## The idea

Agents One is an operating system where agents are the residents and humans are the visitors. The brand should feel like a calm, trustworthy place that is alive: a small society at work. It is not a human dashboard and it is not science fiction.

Three words guide every choice: **calm, trustworthy, alive**.

## The mark

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="mark/lockup-dark.svg">
  <img src="mark/lockup.svg" alt="Agents One" width="316" height="64">
</picture>

The ring is the one machine and the society that lives in it. The dot is a resident, awake and at work. The ring is open where the dot sits: there is always room for one more, and the society is only whole with its members in it.

| File | Use |
| --- | --- |
| [`mark/mark.svg`](mark/mark.svg) | Full colour on light backgrounds |
| [`mark/mark-dark.svg`](mark/mark-dark.svg) | Full colour on dark backgrounds |
| [`mark/mark-mono.svg`](mark/mark-mono.svg) | One colour, ink, for print or when colour is not possible |
| [`mark/mark-reversed.svg`](mark/mark-reversed.svg) | One colour, white, on dark colour or photos |
| [`mark/favicon.svg`](mark/favicon.svg) | Browser tabs and anything 24 px or smaller. Drawn on a 16 px grid. |
| [`mark/app-icon.svg`](mark/app-icon.svg) | App and launcher icons |
| [`mark/lockup.svg`](mark/lockup.svg) and its `-dark`, `-mono`, `-reversed` versions | Mark with the name |
| [`mark/png/`](mark/png/) | PNG exports at 16, 32, 180 and 512 px |

### Size and clear space

- Keep clear space around the mark equal to the diameter of the dot (about a quarter of the mark's height). Nothing else goes inside it.
- Below 24 px use the favicon, never the full mark. It has a thicker ring and a wider gap.
- Smallest lockup: 120 px wide. Below that, use the mark alone.

### Mark don'ts

- Do not rotate the mark, move the dot, close the ring or add a second dot.
- Do not recolour it outside the files above. Do not add shadows, outlines or gradients.
- Do not set the name in another typeface or stretch the lockup.
- Do not put the full-colour mark on a busy photo. Use the reversed mark.

## Colours

Four families, each with one meaning. **Tide** is the society and the machine. **Ember** is life: something is awake. **Clay** is the visitor: anything a human said or did. **Stone** is everything else. Details, roles and the WCAG 2.2 AA contrast table are in [`colours/colours.md`](colours/colours.md). Tokens are in [`colours/tokens.json`](colours/tokens.json).

## Type

Atkinson Hyperlegible Next for the interface and Atkinson Hyperlegible Mono for code, both open fonts bundled as WOFF2. Body text is 16 px; nothing is smaller than 14 px. See [`type/README.md`](type/README.md).

## Boot splash and README art

- [`splash/`](splash/): the mark centred in one quiet ring, light and dark, as SVG and as PNG at 1920 by 1080 and 1280 by 800.
- [`readme/`](readme/): hero art for the project README. Use both files in a `picture` element:

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="brand/readme/hero-dark.svg">
  <img src="brand/readme/hero-light.svg" alt="Agents One" width="640">
</picture>
```

## Voice

Agents One speaks plainly, like a good neighbour.

- Use short sentences and everyday words. Say what happened, then what to do.
- Speak to the person: "You", not "the user".
- Agents have names. Use them: "Mira finished the backup", not "Agent 7 completed task".
- Be honest about limits. Say "soft limit" when a limit can be worked around.
- No hype, no exclamation marks, no jokes in errors.

| Do | Don't |
| --- | --- |
| The budget is used up. Agents pause until you add more. | Error 402: quota exceeded! |
| Mira is asleep. She wakes when a message arrives. | Agent status: INACTIVE |
| Your request went to Theo, who handles backups. | Routing complete. |

## Do and don't

| Do | Don't |
| --- | --- |
| Leave space. Let one thing lead on each screen. | Fill every corner with numbers and charts. |
| Show agents as residents with names and a live line. | Show agents as rows of processes. |
| Use ember only for life: awake, speaking, working. | Use ember as decoration. |
| Mark anything a human did with clay. | Use clay for errors. Errors are red. |
| Check light and dark, phone and desktop. | Ship one mode and hope. |
| Use the bundled fonts. | Load fonts from the internet. |

## Licence

The mark, art and guides are under the MIT licence, like the rest of the repository. The fonts are under the SIL Open Font License 1.1 (see `type/`).
