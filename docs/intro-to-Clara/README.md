# Intro to CLARA — presentation

An introduction to CLARA for a non-technical audience (VISION_AND_REQUIREMENTS.md, "Presentation").

**Open:** [`CLARA_Introduction.pptx`](CLARA_Introduction.pptx) — opens directly in Apple Keynote (File → Open)
and in PowerPoint. 22 slides, 16:9, with speaker notes on every slide.

| Part | Slides | Content |
|---|---|---|
| 1 · Why a new foundation | 2–5 | What FIONA does; what a multi-hospital FIONA needs; the seven REDCap-style requests CLARA answers unchanged |
| 2 · The silent partner | 6–9 | The API: incoming studies, series and transfer requests; a transfer start to finish; keys, validation, audit, documentation |
| 3 · The web interface | 10–17 | Features by theme: projects, forms, data collection, export, oversight, help and languages |
| 4 · Signing in and permissions | 18–21 | Hospital names (named sign-in sources); sign-in methods and protections; roles and permission levels |
| Summary | 22 | Three takeaways and the next steps from the VISION document |

## Folder layout

- `screenshots/` — the application at iPad landscape resolution (1024 × 768 @2x), from a demo installation.
- `tools/capture_screenshots.mjs` — seeds the two demo projects on a fresh `e2e/dev-stack.sh` and captures `screenshots/`.
- `tools/build_presentation.cjs` — generates the deck from `screenshots/` and `tools/icons/`.
- `tools/icons/` — white PNG icons from [Tabler Icons](https://tabler.io/icons) (MIT licence).

## Rebuilding

```sh
# 1. fresh screenshots (optional) — a dev stack on the capture script's default ports
CLARA_API_PORT=8185 CLARA_WEB_PORT=8190 ./e2e/dev-stack.sh          # in one shell
CLARA_WEB_URL=http://127.0.0.1:8190 CLARA_API_URL=http://127.0.0.1:8185 \
  node docs/intro-to-Clara/tools/capture_screenshots.mjs              # in another

# 2. the deck
cd docs/intro-to-Clara/tools && npm install && node build_presentation.cjs
```

The deck uses the macOS fonts Avenir Next and Helvetica Neue, since Keynote is the target; PowerPoint on
Windows substitutes its own. Slide text and speaker notes live in `build_presentation.cjs` — edit them there
and rebuild rather than editing the `.pptx` by hand, or the next rebuild overwrites the edits.
