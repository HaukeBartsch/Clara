# Vendored front-end libraries

Everything here is served same-origin. No page in this application references a
CDN — the installation must work with no internet access (`ASM-TECH-2`,
`REQ-TECH-001`), and CI greps the shipped templates and assets for fetch
references to external hosts.

There is **no build step** here either (`REQ-TECH-001`): these are upstream
distribution files, copied or derived by the commands below and committed as
they land. Nothing generates them at deploy time.

## Bootstrap 5.3.8

| | |
|---|---|
| Source | `assets/bootstrap-5.3.8-dist.zip` (upstream `bootstrap-5.3.8-dist`) |
| Files taken | `css/bootstrap.min.css`, `js/bootstrap.bundle.min.js` |
| Version pin | 5.3.8 (`Design/Technology_Stack_Design.md` §2 fixes the 5.3.x line) |

```sh
unzip -j assets/bootstrap-5.3.8-dist.zip \
  'bootstrap-5.3.8-dist/css/bootstrap.min.css' \
  'bootstrap-5.3.8-dist/js/bootstrap.bundle.min.js' \
  -d web/assets/vendor/bootstrap/
```

The bundle JS includes Popper, so Popper is not vendored separately. RTL and
non-minified variants are deliberately not taken — one stylesheet per page
(`REQ-UI-040`) and one minified script. The archive carries macOS AppleDouble
entries (`__MACOSX/`, `._*`); they are excluded, and `.gitignore` ignores them.

## Themes — Bootswatch `darkly`, `yeti`

| | |
|---|---|
| Source | `assets/darkly_theme_bootstrap.min.css`, `assets/yeti_theme_bootstrap.min.css` |
| Files written | `themes/<name>/bootstrap.min.css` (full replacements, never layered) |

```sh
for t in darkly yeti; do
  sed -E 's~@import url\(["'"'"']?https?://[^)]*\)\s*;?~~g' \
    "assets/${t}_theme_bootstrap.min.css" \
    > "web/assets/vendor/bootstrap/themes/${t}/bootstrap.min.css"
done
```

Each theme is a full replacement for `bootstrap.min.css` (`REQ-TECH-027`), so a
page links exactly one stylesheet — the standard file or one theme file, chosen
server-side (`REQ-UI-040`).

**The Google Fonts `@import` each Bootswatch theme starts with is stripped at
derivation**, which the same requirement mandates: a theme must render completely
offline. The font the themes wanted (Lato for Darkly, Open Sans for Yeti) is not
substituted per theme — `web/assets/app.css` sets Geist as the body font for every
theme (`AGENTS.md` "the web interface font is Geist"), so markup and typography
stay identical across themes.

After re-deriving, check that no *fetch* reference survived — license URLs in the
header comment and the `http://www.w3.org/2000/svg` XML namespace inside inline
SVG data URIs are not fetches and must stay:

```sh
grep -o 'url(http[^)]*\|@import url(http[^)]*' web/assets/vendor/bootstrap/themes/*/bootstrap.min.css
```

## Geist

| | |
|---|---|
| Source | `assets/Geist.zip` |
| Files taken | `Geist-VariableFont_wght.ttf`, `Geist-Italic-VariableFont_wght.ttf`, `OFL.txt` |

```sh
unzip -j assets/Geist.zip \
  'Geist/Geist-VariableFont_wght.ttf' \
  'Geist/Geist-Italic-VariableFont_wght.ttf' \
  'Geist/OFL.txt' \
  -d web/assets/vendor/fonts/geist/
```

The variable weight axis (100–900) means two files cover the family; `@font-face`
rules live in `web/assets/app.css`. **`OFL.txt` ships with the font and must not
be removed** — the SIL Open Font License requires the licence to accompany the
font files. Only the TTFs are available here (the upstream Fontsource package
also offers WOFF2); they are served locally either way.

## Not vendored yet: Tabulator

`AGENTS.md` fixes Tabulator for performant table rendering, but nothing in this
tree renders a table yet — the dashboard's project rows are server-rendered
(`Design/User_Interface_Design.md` §4). It is therefore admitted when the first
table ships (M3 administration lists at the earliest), and that admission should
be recorded as a row in `Design/Technology_Stack_Design.md` §3 with its pinned
version, integrity hash and security-review date (`REQ-TECH-026`) rather than
appearing here unannounced.
