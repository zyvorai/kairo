# Kairo docs site

Built with [Docusaurus](https://docusaurus.io/). Serves the live docs at https://zyvorai.github.io/kairo/.

Points directly at the repo's existing `docs/` folder (`docusaurus.config.ts`'s `docs.path: '../docs'`). This repo's docs/ folder is small today (just ARCHITECTURE.md) — most feature detail lives in the root README, which the homepage links to directly rather than duplicating.

## Local development

```bash
npm install
npm start
```

## Build

```bash
npm run build
npm run serve   # preview the production build locally
```

## Deployment

Deployment is automatic: `.github/workflows/pages.yml` builds and publishes this site to GitHub Pages on every push to `main` that touches `website/`, `docs/`, or the workflow file itself.
