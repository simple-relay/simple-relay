# Documentation Framework Design Specification

- **Project:** `simplerelay.dev` (Documentation & User Guides)
- **Date:** 2026-10-14
- **Author:** pmlonng
- **Status:** Draft / In Review

---

## 1. Intent & The Why

Simple Relay provides reliable HTTP delivery with zero infrastructure friction. Developers who adopt Simple Relay are looking for an immediate, self-contained solution: run a single binary or a single container command, point it at their application, and achieve at-least-once delivery with persistent durability and smart retries.

The documentation must reflect this exact mindset:
1. **Developer First:** Eliminate unnecessary architectural bloat and lengthy theoretical preambles. An engineer must be able to deploy and verify a working relay in under 60 seconds.
2. **"One Binary" Philosophy:** Anchor the product identity strictly on self-contained autonomy ("One binary. No infrastructure friction. Reliable HTTP delivery."). Avoid hard-coupling marketing or docs to specific storage engines (e.g., do not overly dogmatize SQLite or frame it as strictly "anti-Redis/Kafka"), keeping the path open for future enterprise backends.
3. **AI Agent & LLM Native:** Support modern coding agents (Cursor, Windsurf, Claude Code, GitHub Copilot, Antigravity) with structured `/llms.txt` and `/llms-full.txt` files so agents can ingest documentation and configure relays without parsing visual HTML chrome.
4. **Clean, Fast, SEO-Optimized Web Experience:** Maintain parity with `simplerelay.dev`'s clean Apple-inspired design system (typography, dark/light contrast, responsive layouts), providing fast static page delivery via Astro.

---

## 2. Goals, Non-Goals & Anti-Goals

### Goals
- Deliver a comprehensive documentation framework hosted directly on `simplerelay.dev/docs/`.
- Fix the landing page navigation links so all "Docs" and "Get Started" CTAs smoothly link into `/docs/`.
- Provide a clear, intuitive 4-tier information hierarchy:
  1. *Getting Started* (Overview, 60s Quickstart).
  2. *Core Concepts* (Inbound Webhooks, Outbound Proxy, Retries & DLQ).
  3. *Recipes & Guides* (Stripe/GitHub webhooks, Rate limiting & 429 backoff, Production deployment).
  4. *Reference* (CLI flags, Environment variables, REST API spec, Embedded UI guide).
- Generate `/public/llms.txt` (curated index + quick specs) and `/public/llms-full.txt` (flattened full documentation context) at build time or statically.
- Implement structured SEO metadata (canonical tags, Open Graph, JSON-LD `TechArticle` / `SoftwareApplication`).

### Non-Goals
- Building an interactive interactive live API console or sandbox server within the static docs.
- Rewriting the landing page (`src/pages/index.astro`); only update broken links and integrate layout coherence.
- Writing all eventual deep-dive enterprise guides in phase 1 (phase 1 establishes the complete framework, layouts, navigation, and initial foundational documentation pages).

### Anti-Goals
- Over-engineering with heavy client-side documentation frameworks (e.g., heavy SPA runtimes, bloated search indexes, or external search SaaS).
- Dogmatic claims that lock Simple Relay into a single embedded database forever, preventing enterprise storage adapters down the road.

---

## 3. Constraints & Technical Decisions

- **Framework:** Astro 5 with Tailwind CSS (matching the existing repo dependencies).
- **Content Format:** Standard Markdown (`.md`) or Astro Content Collections (`src/content/docs/*.md`).
- **Styling:** Follow `DESIGN.md` in `simplerelay.dev` (Inter font, SF Pro display tracking, slate/dark accents, clean code syntax blocks with copy button).
- **Navigation & Layout:**
  - Sticky header with global navigation and GitHub repo link.
  - Collapsible / responsive left sidebar for doc sections.
  - Main article body with prose readability (65-75ch optimal line length).
  - Right-side on-page Table of Contents (ToC) for headings `h2`, `h3`.
- **AI Crawler Support:**
  - Standard `/llms.txt` following the `llmstxt.org` specification.
  - Comprehensive `/llms-full.txt` containing the full concatenated markdown documentation for one-shot LLM ingestion.

---

## 4. Information Architecture & Content Hierarchy

```
/docs/
├── 1. Getting Started
│   ├── overview.md          # Philosophy of "One binary", architecture overview
│   └── quickstart.md        # Single docker run / binary command, first relay in < 60s
├── 2. Core Concepts
│   ├── inbound-webhooks.md  # Persist-before-ACK durability, raw payload guarantees
│   ├── outbound-proxy.md    # Sync & async modes, client-disconnect cancellation
│   └── retries-and-dlq.md   # Exponential backoff, full jitter, DLQ replay
├── 3. Recipes & Guides
│   ├── stripe-github.md     # Ingesting webhooks with HMAC signature verification
│   ├── rate-limits-429.md   # Handling upstream rate limits and Retry-After
│   └── production-deploy.md # Systemd, Docker Compose, automated backups
└── 4. Reference
    ├── configuration.md     # CLI arguments & environment variable matrix
    ├── api-reference.md     # REST endpoints for ingest, DLQ inspection, and replay
    └── embedded-ui.md       # Inspecting deliveries and replaying via the built-in UI
```

---

## 5. What is Left to the Builder

- Choosing between Astro Content Collections (`astro:content`) vs dynamic file routing for loading markdown files.
- Exact syntax highlighting theme configuration (e.g. Shiki with `github-dark` or `nord`).
- Automated script or Astro integration endpoint for generating `/llms.txt` and `/llms-full.txt` from documentation content sources.
- Mobile drawer toggle animation and mobile sidebar navigation implementation.
