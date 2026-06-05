---
name: Obsidian Protocol
colors:
  surface: '#131313'
  surface-dim: '#131313'
  surface-bright: '#393939'
  surface-container-lowest: '#0e0e0e'
  surface-container-low: '#1b1b1b'
  surface-container: '#1f1f1f'
  surface-container-high: '#2a2a2a'
  surface-container-highest: '#353535'
  on-surface: '#e2e2e2'
  on-surface-variant: '#becbb0'
  inverse-surface: '#e2e2e2'
  inverse-on-surface: '#303030'
  outline: '#89957c'
  outline-variant: '#3f4a36'
  surface-tint: '#71e006'
  primary: '#8bfd34'
  on-primary: '#173800'
  primary-container: '#70df04'
  on-primary-container: '#2b5d00'
  inverse-primary: '#326b00'
  secondary: '#c9c6c5'
  on-secondary: '#313030'
  secondary-container: '#4a4949'
  on-secondary-container: '#bab8b7'
  tertiary: '#e4e1e1'
  on-tertiary: '#313030'
  tertiary-container: '#c8c5c5'
  on-tertiary-container: '#525252'
  error: '#ffb4ab'
  on-error: '#690005'
  error-container: '#93000a'
  on-error-container: '#ffdad6'
  primary-fixed: '#8cfd34'
  primary-fixed-dim: '#71e006'
  on-primary-fixed: '#0b2000'
  on-primary-fixed-variant: '#245100'
  secondary-fixed: '#e5e2e1'
  secondary-fixed-dim: '#c9c6c5'
  on-secondary-fixed: '#1c1b1b'
  on-secondary-fixed-variant: '#474646'
  tertiary-fixed: '#e5e2e1'
  tertiary-fixed-dim: '#c8c6c5'
  on-tertiary-fixed: '#1c1b1b'
  on-tertiary-fixed-variant: '#474746'
  background: '#131313'
  on-background: '#e2e2e2'
  surface-variant: '#353535'
typography:
  headline-xl:
    fontFamily: Sora
    fontSize: 48px
    fontWeight: '800'
    lineHeight: '1.1'
    letterSpacing: 0.05em
  headline-lg:
    fontFamily: Sora
    fontSize: 32px
    fontWeight: '700'
    lineHeight: '1.2'
    letterSpacing: 0.02em
  headline-lg-mobile:
    fontFamily: Sora
    fontSize: 24px
    fontWeight: '700'
    lineHeight: '1.2'
  headline-md:
    fontFamily: Sora
    fontSize: 20px
    fontWeight: '600'
    lineHeight: '1.4'
  body-lg:
    fontFamily: JetBrains Mono
    fontSize: 16px
    fontWeight: '400'
    lineHeight: '1.6'
  body-md:
    fontFamily: JetBrains Mono
    fontSize: 14px
    fontWeight: '400'
    lineHeight: '1.5'
  label-sm:
    fontFamily: JetBrains Mono
    fontSize: 11px
    fontWeight: '500'
    lineHeight: '1'
    letterSpacing: 0.1em
  code-snippet:
    fontFamily: JetBrains Mono
    fontSize: 13px
    fontWeight: '400'
    lineHeight: '1.4'
spacing:
  unit: 4px
  gutter: 16px
  margin-mobile: 16px
  margin-desktop: 32px
  container-max: 1440px
---

## Brand & Style

The design system is an industrial-grade, high-fidelity framework tailored for the cybersecurity domain. It evokes a "fail-closed" security posture—stable, impenetrable, and authoritative. The visual language centers on **High-Contrast Brutalism** mixed with **Futuristic Technical** elements. It targets SOC analysts and security engineers who require immediate clarity and a sense of absolute control over complex data environments.

The emotional response should be one of "vigilant precision." Every UI element must feel like it belongs in a mission-critical terminal. The aesthetic is defined by pure blacks, wireframe structures, and sharp geometries that suggest a high-performance, low-latency engine running underneath the surface.

## Colors

This design system utilizes an "Ink-and-Neon" palette. The background is strictly pure black (#000000) to maximize contrast and reduce eye strain in low-light environments. 

- **Primary Neon Green (#70DF04):** Used exclusively for actionable elements, critical borders, and live data stream highlights.
- **Surface Obsidian (#0A0A0A):** Used for primary container backgrounds to provide a subtle distinction from the base layer.
- **Surface Charcoal (#1A1A1A):** Used for secondary UI elements like headers, nav rails, and hover states.
- **Neutral Grays:** Use high-chroma grays sparingly for non-essential technical labels to maintain the focus on primary green and pure black.

## Typography

The typography system follows a dual-font strategy. **Sora** (as a high-quality alternative to wide geometric sans-serifs) is used for high-level headers to mimic the futuristic, wide-set look of the brand wordmark. All headers should be treated with tight line height and generous letter spacing for a technical, cinematic feel.

**JetBrains Mono** handles all functional roles. It is the workhorse for logs, metrics, data tables, and body copy. Monospaced characters ensure that columns of data align perfectly across the horizontal axis, facilitating rapid scanning of security logs. All labels should be set in uppercase with increased letter spacing to enhance the "instrument panel" aesthetic.

## Layout & Spacing

This design system uses a **Fixed 12-Column Grid** for desktop and a **Fluid 4-Column Grid** for mobile. The layout is inspired by industrial command centers—highly dense with minimal wasted space.

- **Spacing Rhythm:** Based on a strict 4px base unit. 
- **Density:** High. Margins between logical groups should be kept to a minimum (16px or 24px) to keep data within the user's primary field of view.
- **Terminals:** Large-scale components like log viewers or graph dashboards should span 8-12 columns, while utility panels (sidebar, inspectors) should occupy 2-4 columns.
- **Borders:** Use 1px borders as primary separators instead of spacing. This "wireframe" approach reinforces the technical nature of the product.

## Elevation & Depth

Depth is conveyed through **Tonal Layering** and **High-Contrast Outlines** rather than shadows. In a "fail-closed" environment, shadows are considered "visual noise."

- **Layer 0 (Background):** Pure black (#000000).
- **Layer 1 (Cards/Panels):** Dark Obsidian (#0A0A0A) with a 1px primary green (#70DF04) or dark gray (#333333) border.
- **Layer 2 (Popovers/Modals):** Charcoal (#1A1A1A) with a mandatory 1px primary green border to signify focus.
- **Accents:** Use primary green glow effects (2-4px blur) very sparingly, only for critical status indicators or "active" laser-line animations.

## Shapes

The shape language is strictly **Sharp (0px roundedness)**. This reinforces the industrial, precise, and uncompromising nature of a security system. 

- **Buttons:** Perfectly rectangular.
- **Inputs:** Sharp corners with 1px outlines.
- **Status Indicators:** Squares or diamonds instead of circles to maintain the geometric rigidity.
- **Exceptions:** Icons may contain curves for legibility, but they should be housed within sharp-cornered containers.

## Components

- **Buttons:** Should be high-contrast blocks. Primary CTAs are solid Primary Green with Black text. Secondary buttons are Black with 1px Green borders.
- **Input Fields:** Use 1px borders that glow (soft outer glow) when focused. Labels must always sit above the field in uppercase monospace.
- **Data Tables:** Zebra-striping is prohibited. Use 1px horizontal dividers. The header row should have a primary green bottom border.
- **Status Chips:** Rectangular tags with high-contrast text. "CRITICAL" status should use the Primary Green background or Red for urgent alerts.
- **Terminals:** Components housing logs should have a subtle scan-line overlay (0.05 opacity) and use only JetBrains Mono.
- **Progress Bars:** Use segmented blocks (e.g., 10 separate blocks) instead of a continuous fill to emphasize the digital/discrete nature of the data.