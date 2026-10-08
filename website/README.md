# Cercano website

The single-page website for Cercano. One source tree supports both the existing
Sites deployment and a static GitHub Pages export.

```bash
npm install
npm run dev
npm test
npm run test:pages
```

The page source lives in `app/`. Theme colors mirror the CLI's `cr4k3r_j4x`
and `daylight` palettes.

## GitHub Pages

`npm run build:pages` generates a static export in `out/`, configured for the
project URL `https://cercano-ai.github.io/Cercano/`. The export includes the
`/Cercano` base path in framework and video URLs.

The repository workflow at `.github/workflows/pages.yml` builds, verifies, and
deploys that directory whenever website files change on `main`. This repository
is already configured to publish with GitHub Actions, so future deployments are
automatic after the website branch is merged. Forks must enable GitHub Actions
once under **Settings → Pages → Build and deployment → Source**.

When a custom domain is introduced, update `NEXT_PUBLIC_BASE_PATH` and
`NEXT_PUBLIC_SITE_URL` in the `build:pages` script together.
