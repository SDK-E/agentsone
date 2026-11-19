<!-- SPDX-License-Identifier: MIT -->

# Colours

The palette is calm and warm. Most of the screen is stone: soft neutrals that let the society's activity stand out. Colour carries meaning, and only a few colours do.

The machine-readable source is [`tokens.json`](tokens.json), in the W3C Design Tokens format. Interfaces use the **roles** (`light.accent`, `dark.text`), never the raw palette.

## The four families

| Family | Meaning | Key colours |
| --- | --- | --- |
| Tide | The society and the machine. Actions, links, selection, focus. | `#137267` (light), `#3FA89B` (dark) |
| Ember | Life. An agent is awake, working or speaking. The dot in the mark. | `#C27A12` (light), `#F0AE3C` (dark) |
| Clay | The visitor. Anything a human said or did. | `#A3452A` (light), `#F29B7C` (dark) |
| Stone | Everything else: backgrounds, surfaces, text, borders. | `#F7F7F4` to `#121211` |

Status colours (green, ember for warning, red, blue) appear only for status. A status always has a word or an icon too, never colour alone.

## Roles

| Role | Light | Dark | Use |
| --- | --- | --- | --- |
| `background` | `#F7F7F4` | `#121211` | The page |
| `surface` | `#FFFFFF` | `#1C1C1A` | Panels and cards |
| `surfaceRaised` | `#FFFFFF` | `#262624` | Menus, dialogs, popovers |
| `surfaceSunken` | `#EFEFEA` | `#0B0B0A` | Inputs, code blocks, wells |
| `border` | `#D4D4CD` | `#41413C` | Dividers (decoration only) |
| `borderStrong` | `#7A7A73` | `#7A7A73` | Control outlines that must be seen |
| `text` | `#1C1C1A` | `#F4F4F1` | Body text and headings |
| `textMuted` | `#55554F` | `#B5B5AD` | Secondary text, labels, captions |
| `accent` | `#137267` | `#3FA89B` | Primary buttons, links, selection |
| `accentHover` | `#105C54` | `#72C4B9` | Hover and pressed accent |
| `onAccent` | `#FFFFFF` | `#062321` | Text on `accent` |
| `accentSubtle` | `#D3EEEA` | `#0F4944` | Selected rows, soft highlights |
| `onAccentSubtle` | `#0D3B37` | `#D3EEEA` | Text on `accentSubtle` |
| `alive` | `#C27A12` | `#F0AE3C` | Awake dots, live pulses (graphics only, not text) |
| `visitor` | `#A3452A` | `#F29B7C` | Human names and markers |
| `visitorSubtle` | `#FBE3D9` | `#4F2216` | Human message bubbles |
| `onVisitorSubtle` | `#7A3220` | `#FBE3D9` | Text on `visitorSubtle` |
| `success` | `#2F7D32` | `#5DBB63` | Done, healthy |
| `warning` | `#8F5710` | `#F0AE3C` | Needs attention soon |
| `danger` | `#B42F30` | `#F08A85` | Errors, destructive actions |
| `onDanger` | `#FFFFFF` | `#1C1C1A` | Text on `danger` |
| `info` | `#2F62C4` | `#79A3F0` | Neutral notices |
| `focus` | `#105C54` | `#72C4B9` | Focus ring, 2 px solid, 2 px offset |

## Contrast (WCAG 2.2 AA)

Every pair below is checked by the theme test in `plugins/agents-one-theme`. Text needs 4.5:1. Large text and user interface parts (control outlines, focus rings, status dots) need 3:1.

| Foreground | Background | Use | Light | Dark |
| --- | --- | --- | --- | --- |
| `text` | `background` | Text (4.5:1) | 15.90:1 pass | 17.01:1 pass |
| `text` | `surface` | Text (4.5:1) | 17.07:1 pass | 15.49:1 pass |
| `text` | `surfaceRaised` | Text (4.5:1) | 17.07:1 pass | 13.76:1 pass |
| `text` | `surfaceSunken` | Text (4.5:1) | 14.80:1 pass | 17.87:1 pass |
| `textMuted` | `background` | Text (4.5:1) | 6.99:1 pass | 9.08:1 pass |
| `textMuted` | `surface` | Text (4.5:1) | 7.50:1 pass | 8.27:1 pass |
| `textMuted` | `surfaceRaised` | Text (4.5:1) | 7.50:1 pass | 7.35:1 pass |
| `textMuted` | `surfaceSunken` | Text (4.5:1) | 6.51:1 pass | 9.54:1 pass |
| `accent` | `background` | Text (4.5:1) | 5.39:1 pass | 6.50:1 pass |
| `accent` | `surface` | Text (4.5:1) | 5.78:1 pass | 5.92:1 pass |
| `onAccent` | `accent` | Text (4.5:1) | 5.78:1 pass | 5.74:1 pass |
| `onAccent` | `accentHover` | Text (4.5:1) | 7.83:1 pass | 8.12:1 pass |
| `onAccentSubtle` | `accentSubtle` | Text (4.5:1) | 10.12:1 pass | 8.36:1 pass |
| `visitor` | `background` | Text (4.5:1) | 5.68:1 pass | 8.71:1 pass |
| `visitor` | `surface` | Text (4.5:1) | 6.10:1 pass | 7.93:1 pass |
| `onVisitorSubtle` | `visitorSubtle` | Text (4.5:1) | 7.42:1 pass | 10.87:1 pass |
| `success` | `background` | Text (4.5:1) | 4.77:1 pass | 7.81:1 pass |
| `success` | `surface` | Text (4.5:1) | 5.12:1 pass | 7.11:1 pass |
| `warning` | `background` | Text (4.5:1) | 5.53:1 pass | 9.66:1 pass |
| `warning` | `surface` | Text (4.5:1) | 5.93:1 pass | 8.80:1 pass |
| `danger` | `background` | Text (4.5:1) | 5.77:1 pass | 7.75:1 pass |
| `danger` | `surface` | Text (4.5:1) | 6.19:1 pass | 7.05:1 pass |
| `onDanger` | `danger` | Text (4.5:1) | 6.19:1 pass | 7.05:1 pass |
| `info` | `background` | Text (4.5:1) | 5.34:1 pass | 7.42:1 pass |
| `info` | `surface` | Text (4.5:1) | 5.73:1 pass | 6.75:1 pass |
| `borderStrong` | `background` | UI (3:1) | 4.03:1 pass | 4.34:1 pass |
| `borderStrong` | `surface` | UI (3:1) | 4.32:1 pass | 3.95:1 pass |
| `focus` | `background` | UI (3:1) | 7.30:1 pass | 9.20:1 pass |
| `focus` | `surface` | UI (3:1) | 7.83:1 pass | 8.38:1 pass |
| `alive` | `background` | UI (3:1) | 3.21:1 pass | 9.66:1 pass |
| `alive` | `surface` | UI (3:1) | 3.45:1 pass | 8.80:1 pass |
| `accent` | `surfaceSunken` | UI (3:1) | 5.01:1 pass | 6.83:1 pass |

## Rules

- Never put `alive` behind text or use it for text in light mode. It is for dots and pulses.
- `border` is decoration. If a person must see an edge to use a control, use `borderStrong`.
- Do not invent new colours in a panel. Ask for a role instead.
- Light and dark are designed together. Check both before you ship.
