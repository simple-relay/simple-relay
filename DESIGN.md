# Simple Relay — UI Design System & Pattern Specifications

> **Scope:** Design tokens, layout blueprints, color palettes, and interaction patterns for Simple Relay dashboard and downstream interfaces.  
> **Target Audience:** Systems engineers, backend developers, SREs, and platform operators.  
> **Philosophy:** High-density, utilitarian, low-fatigue dark mode. Purpose-built for operational diagnostics rather than constant wallboard monitoring. Zero superficial design bloat.

---

## 1. Design Philosophy & Guidelines

1. **Engineer-Centric Utilitarianism:**
   - Operational tools are used for troubleshooting, auditing, and configuration. Interface density must favor information clarity, tabular data scanning, and rapid triage.
   - Avoid generic AI/SaaS tropes: no pastel gradients, no bubbly oversized drop shadows, and no decorative cards with low information density.

2. **Eye-Fatigue Prevention & Accessible Contrast:**
   - Pure black (`#000000`) creates jarring, high-contrast halation on OLED and LED displays. Pure white on pure black causes rapid eye strain.
   - Base canvas uses rich dark slate (`#0b0f17`) with carefully tiered surface elevations.
   - All text and badge color pairings strictly maintain WCAG AAA contrast compliance (> 7:1) for critical data points and WCAG AA (> 4.5:1) for metadata.

3. **Zero-Build, Self-Contained Deliverable:**
   - Fully client-side via vendored Alpine.js and Tailwind CSS (`ui/static/`).
   - Requires zero Node.js/npm toolchains and operates completely offline / air-gapped.

---

## 2. Color Palette & Design Tokens

### 2.1 Surfaces & Elevation Tiers

| Token Name | HEX Code | CSS Variable / Utility | Role & Application |
| :--- | :--- | :--- | :--- |
| **Canvas** | `#0b0f17` | `.bg-app-canvas` | Root viewport background and scrollable main content area |
| **Sidebar** | `#070a10` | `.bg-app-sidebar` | Left navigation bar, table headers, sticky section headers |
| **Surface** | `#121824` | `.bg-app-surface` | Table containers, cards, filter bars, resting modal frames |
| **Elevated** | `#182132` | `.bg-app-elevated` | Slide-over inspection drawers, active table row, hover states |
| **Input** | `#0e141f` | `.bg-app-input` | Form inputs, select dropdowns, code block backgrounds |
| **Row Hover** | `#192233` | `.bg-app-row:hover` | Table row hover highlight (subtle visual feedback) |

### 2.2 Borders & Separators

| Token Name | HEX Code | CSS Utility | Usage |
| :--- | :--- | :--- | :--- |
| **Border Primary** | `#243044` | `.border-app` | Card perimeters, drawer dividers, table row borders |
| **Border Subtle** | `#1a2333` | `.border-app-subtle` | Sub-section delimiters, nested groupings |
| **Active Focus** | `#3b82f6` | `focus:border-blue-500` | Focused text inputs, select dropdowns, active row accent |

### 2.3 Typography & Text Tiers

| Level | Tailwind Class | Color / HEX | Application |
| :--- | :--- | :--- | :--- |
| **Heading** | `text-white font-bold` | `#ffffff` | Page titles, primary slugs, modal titles, counts |
| **Body / Primary** | `text-gray-200` | `#e5e7eb` | Table cell data, form labels, drawer body text |
| **Secondary / Labels**| `text-gray-300` / `text-gray-400` | `#cbd5e1` / `#9ca3af`| Table headers, descriptive subtitles, helper text |
| **Muted / Timestamp** | `text-gray-500` | `#6b7280` | Relative time hints, inactive toggles, footer endpoints |
| **Brand Accent Link** | `text-blue-400 hover:text-white` | `#60a5fa` | Monospace IDs, inspection links, clickable paths |

### 2.4 Semantic Status Badges

All status badges use semi-transparent tinted backgrounds with high-contrast text and a matching border:

| Status State | Background | Text Color | Border Color | Typical Context |
| :--- | :--- | :--- | :--- | :--- |
| **Success** | `rgba(6, 78, 59, 0.45)` | `#4ade80` (green-400) | `#15803d` (green-700) | `HTTP 2xx`, `status: success`, Active API key, Inbound route |
| **Failed / Error** | `rgba(159, 18, 57, 0.45)`| `#fb7185` (rose-400) | `#be123c` (rose-700) | `HTTP 4xx/5xx`, `status: failed`, Ingestion error |
| **Warning** | `rgba(146, 64, 14, 0.45)`| `#facc15` (amber-400) | `#b45309` (amber-700) | `status: processing`, Rate-limited upstream retry |
| **In-Flight / Queued**| `rgba(107, 33, 168, 0.45)`| `#c084fc` (purple-400) | `#7e22ce` (purple-700) | `status: dlq`, Outbound async proxy, Enqueued job |
| **Muted / Neutral** | `#1e293b` | `#cbd5e1` (slate-300) | `#334155` (slate-700) | Revoked API key, Inactive rule, Optional header |

```css
/* Badge CSS Definitions */
.bg-status-success { background-color: rgba(6, 78, 59, 0.45) !important; color: #4ade80 !important; border-color: #15803d !important; }
.bg-status-failed  { background-color: rgba(159, 18, 57, 0.45) !important; color: #fb7185 !important; border-color: #be123c !important; }
.bg-status-warning { background-color: rgba(146, 64, 14, 0.45) !important; color: #facc15 !important; border-color: #b45309 !important; }
.bg-status-purple  { background-color: rgba(107, 33, 168, 0.45) !important; color: #c084fc !important; border-color: #7e22ce !important; }
.bg-status-muted   { background-color: #1e293b !important; color: #cbd5e1 !important; border-color: #334155 !important; }
```

---

## 3. Typography & Numerical Data Rules

- **Primary Sans Font:** System native stack:
  ```css
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  ```
- **Monospace Stack & Tabular Numbers:**
  - Used for ULID / UUID IDs, HTTP verbs, paths, status codes, timestamps, latency metrics, and payload previews.
  - **Crucial Rule:** Must enforce tabular figures (`tabular-nums`) to prevent horizontal jitter during live dashboard polling:
  ```css
  pre, code, .font-mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
    font-feature-settings: "tnum";
    font-variant-numeric: tabular-nums;
  }
  ```

---

## 4. Application Shell & Layout Architecture

```text
┌────────────────────────────────────────────────────────────────────────┐
│ Top Toast Notifications (Fixed Top-Right)                             │
├───────────────┬────────────────────────────────────────────────────────┤
│ SIDEBAR (240px│ MAIN VIEWPORT (flex-1 h-full max-h-screen)             │
│ h-screen fixed│                                                        │
│               │ ┌────────────────────────────────────────────────────┐ │
│ [Brand/Pulse] │ │ View Header & Action Bar                           │ │
│               │ ├────────────────────────────────────────────────────┤ │
│ [Nav Items]   │ │ Filter & Search Toolbar                            │ │
│ • Routes      │ ├────────────────────────────────────────────────────┤ │
│ • Requests    │ │ Data Table (High Density, Zebra/Hover)             │ │
│ • Dead letter │ │                                                    │ │
│ • API Keys    │ │                                                    │ │
│               │ │                                                    │ │
│ ───────────── │ │                                                    │ │
│ [Footer Nav]  │ └────────────────────────────────────────────────────┘ │
│ • Docs        │                                                        │
│ • Report      │ ┌───────────────────────────────────┐                 │
│ • Sign Out    │ │ Slide-Over Inspection Drawer      │                 │
│ ───────────── │ │ (Right-aligned, Non-blocking,     │                 │
│ v0.8.0 /health│ │  Esc-closable, Switches on click) │                 │
└───────────────┴─┴───────────────────────────────────┴─────────────────┘
```

### 4.1 Shell Constraints
1. **Viewport-Height Lock (`h-screen max-h-screen overflow-hidden`):**
   - The outer shell (`<body>`) never expands beyond 100% viewport height.
   - Prevents double scrollbars. The main content area independently scrolls with custom dark scrollbars.
2. **Fixed Sidebar (`w-60 shrink-0 h-full max-h-screen bg-app-sidebar`):**
   - Retains 100% height regardless of how long the main table content is.
   - Pinned footer stays docked at the bottom (`shrink-0`).

### 4.2 Sidebar Hierarchy & Ordering
1. **Header:** Brand SVG logo, Title "Simple Relay", live auto-refresh polling indicator.
2. **Main Navigation Links:**
   - **Routes:** Route configuration & policies.
   - **Requests:** Ingestion and outbound proxy audit log (with live count badge).
   - **Dead letter:** DLQ triage (with count badge; highlights red with `animate-pulse` when count > 0).
   - **API Keys:** Located directly under Dead letter (unified navigation block).
3. **Sidebar Footer (Clean Text Links):**
   - Uses plain text links with subtle hover transitions (`text-gray-400 hover:text-white`).
   - **No external link redirect icons (`↗`):** Removed to keep the UI clean and distraction-free.
   - **Links Specification:**
     - `Documentation`: Opens `https://simplerelay.dev/docs/` in a new tab (`target="_blank" rel="noopener noreferrer"`).
     - `Report issues`: Opens `https://github.com/simple-relay/simple-relay/issues` in a new tab.
     - `Sign Out`: Triggered via API logout when authentication is active.
   - **Micro-Bar Bottom:**
     - Left: Monospace version linking to `CHANGELOG.md`.
     - Right: Direct link to `/healthz`.

---

## 5. Interaction Patterns & Component Behavior

### 5.1 Slide-Over Drawer vs. Split Screen
- **Decision:** Use a slide-over inspection drawer on the right rather than a side-by-side split screen.
- **Rationale:** Engineers do not continuously monitor this interface. Side-by-side split view permanently deprives tables of horizontal width, truncating critical URLs, route IDs, and error strings.
- **Drawer Dimensions:**
  - Mobile (`< 768px`): `100vw` (full screen).
  - Tablet / Small Desktop (`>= 768px`): `62vw` (max `860px`).
  - Large Desktop (`>= 1280px`): `52vw` (max `920px`).

### 5.2 Seamless Drawer Row-Click Switching (Critical Pattern)
- **Problem:** Standard modal/drawer libraries close the drawer when clicking outside, requiring two clicks to inspect a different row (one click to close, another to open the new item).
- **Solution:** Table rows attach a `data-drawer-row` attribute. The outside click handler intercepts and ignores clicks originating from other drawer rows:

```html
<!-- Table Row -->
<tr class="bg-app-row transition-colors cursor-pointer"
    data-drawer-row
    :class="drawerOpen && activeRequestId === item.id ? 'bg-app-elevated border-l-2 border-blue-500' : ''"
    @click="openRequestDrawer(item.id)">
  ...
</tr>

<!-- Drawer Container -->
<div class="fixed inset-y-0 right-0 z-50 flex drawer-container pointer-events-none"
     x-show="drawerOpen"
     @keydown.escape.window="closeDrawer()">
  <div class="w-full bg-app-surface border-l border-app pointer-events-auto"
       @click.outside="handleDrawerClickOutside($event)">
    ...
  </div>
</div>
```

```javascript
// Alpine.js Handler
handleDrawerClickOutside(e) {
  if (!this.drawerOpen) return;
  // If the click occurred on another table row, let that row's click handler run
  if (e && e.target && e.target.closest && e.target.closest('[data-drawer-row]')) {
    return;
  }
  this.closeDrawer();
}
```

### 5.3 DLQ Row-Click vs. Replay Action
- In the Dead Letter Queue table, clicking anywhere on a row opens the inspection drawer for deep diagnostics.
- The **Replay** button at the end of the row uses `@click.stop="triggerReplaySingle(dlq.id)"` to prevent drawer opening and execute replay immediately.

### 5.4 High-Density Tables
- Table headers use uppercase `text-[11px] font-semibold text-gray-300` on `.bg-app-sidebar`.
- Table rows use `py-3` or `py-3.5` padding with `.border-app` dividers.
- Hover state `.bg-app-row:hover` (`#192233`) provides subtle line recognition.
- Text overflow handling: Long destinations and error messages are truncated (`truncate max-w-xs` or `max-w-sm`) with native HTML `title` attributes for full inspection on hover.

---

## 6. Release Automation Preservation Invariant

When displaying version information in the UI, release automation tools (specifically Google's `release-please`) rely on HTML comment annotations.

```html
<!-- CORRECT: Anchor tag is preserved intact during version bumps -->
<a href="https://github.com/simple-relay/simple-relay/blob/main/CHANGELOG.md" 
   target="_blank" 
   rel="noopener noreferrer" 
   class="text-gray-400 hover:text-white font-mono transition-colors" 
   title="View CHANGELOG.md">
  v0.8.0<!-- x-release-please-version -->
</a>
```

- **Invariant:** The comment `<!-- x-release-please-version -->` must sit directly adjacent to the semver text inside the anchor.
- `release-please` generic updaters scan for the comment and substitute only the leading semver token, preserving the wrapping `<a>` tag and `href` intact.

---

## 7. Implementation Checklist for New / Downstream UIs

When building or updating other UIs in this ecosystem, ensure:
- [ ] Root shell is locked to 100% viewport height (`h-screen max-h-screen overflow-hidden`).
- [ ] Base background is `#0b0f17`, sidebar is `#070a10`, surface cards are `#121824`.
- [ ] Borders use `#243044` (`.border-app`).
- [ ] Monospace fonts apply `font-variant-numeric: tabular-nums` to eliminate jitter.
- [ ] Inspection views use slide-over drawers with non-blocking row click switching (`data-drawer-row`).
- [ ] Secondary links in navigation footers are plain text without external redirect icons.
- [ ] Version labels link to `CHANGELOG.md` with `<!-- x-release-please-version -->`.
- [ ] Zero Node.js build dependencies; vendor static CSS and JS.
