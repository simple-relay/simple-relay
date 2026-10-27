# simplerelay.dev

Landing page and documentation for [**Simple HTTP Relay**](https://github.com/simple-relay/simple-relay):  
> *"One binary. No infrastructure. Reliable HTTP delivery."*

Built with [Astro](https://astro.build) (SSG) & [Tailwind CSS](https://tailwindcss.com).

## Project Structure

```text
├── .github/workflows/
│   └── deploy.yml          # GitHub Actions build & deploy to GitHub Pages
├── public/
│   └── CNAME               # Custom domain (simplerelay.dev)
├── src/
│   ├── layouts/
│   │   └── Layout.astro    # Base layout (Header, Meta, Footer)
│   └── pages/
│       ├── index.astro     # Landing page (Hero section)
│       └── docs/
│           └── index.astro # Documentation starter page
├── astro.config.mjs        # Astro configuration
├── tailwind.config.mjs     # Tailwind CSS configuration
└── package.json
```

## Local Development

```bash
# Install dependencies
npm install

# Start local dev server
npm run dev

# Build for production
npm run build
```
