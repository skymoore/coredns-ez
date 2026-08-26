# Design

Register: product. CoreDNS operator admin on the DoH listener.

## Brand

Official coredns.io colors:

| Token | Hex |
|---|---|
| Dark | `#280071` |
| Middle | `#5F259F` |
| Light | `#8246AF` |

Typeface: Lato (self-hosted woff2), stand-in for Brandon Grotesque in the logo.
Logos: Colour Icon SVG, Colour Horizontal PNG, from coredns/logo and coredns.io.

## Dials

DESIGN_VARIANCE 3, MOTION_INTENSITY 3, VISUAL_DENSITY 7.

## Shape

Controls 8px, panels 12px. Do not exceed 16px on cards. Buttons may be slightly rounder than panels.

## Color

Light canvas `#f6f4fb` (chroma toward 277, not cream). Ink `#1b0840`.
Dark canvas `#120526` (never `#000`). Elevated `#1c0b38`.
Primary action: middle in light, light in dark.
Accent is used for current nav, primary buttons, and focus rings only.

Semantic: success `#2f6f3e` / `#8fce9a`, warning `#9a6700` / `#e8c547`, danger `#b42318` / `#f0a8a0`.

## Type

Fixed rem scale, ratio ~1.2. Body 14px. Page titles 22px. Tabular nums on serials, QPS, TTL.

## Motion

150-250ms, opacity/transform only. Honor `prefers-reduced-motion`.

## Z-index

base 0, sticky 20, dropdown 30, overlay 40, modal 50, toast 60, tooltip 70.

## Anti-references

Default shadcn zinc, Inter, AI-purple glow, three equal metric cards, Lucide mixed with Phosphor, JWT in localStorage, em-dashes in copy.
