# Job: Documentation site

Version: v1.0 · Epic: 04 Release engineering · Status: planned · Depends on: v0.2 10-*

## Goal

Publish `docs/` as a browsable site with the API reference rendered from the OpenAPI
document, versioned per release.

## Tasks

- [ ] A static generator that works from the existing Markdown without restructuring
      (for example MkDocs with Material, or Hugo), built in CI and published to GitHub
      Pages per tag.
- [ ] OpenAPI rendered with a static renderer; the roadmap folder included as-is for
      transparency.

## Acceptance criteria

- The site is live at the tag with the install guide as its entry page.
