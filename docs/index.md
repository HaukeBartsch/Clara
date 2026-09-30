---
title: |
  \begin{tcolorbox}[colframe=white,opacityfill=0.4,arc=2pt,boxsep=2pt,width=\textwidth,fontupper=\linespread{.9}\selectfont]
  Clara - Clinical Logbook for Automated Research Assistance
  \end{tcolorbox}
subtitle: |
  \begin{tcolorbox}[colback=white,opacityfill=0.4,colframe=white,arc=2pt,boxsep=2pt,width=\textwidth,fontupper=\linespread{.9}\selectfont]
  API Requirements, Plan and Design Documentation \\
  v1.0.0, Hauke Bartsch 2026-09-30
  \end{tcolorbox}
keywords: [API, Software, Documentation]
toc: true
toc-depth: 2
numbersections: false
geometry: "margin=1.5in"
titlepage: true
titlepage-color: "000000"
titlepage-text-color: "222222"
titlepage-rule-color: "1e1e1e"
titlepage-rule-height: 4
titlepage-background: "background-wide.png"
listings-no-page-break: true
code-block-font-size: \footnotesize
# Inject LaTeX to make a transparent box behind text
header-includes:
  - \usepackage{tcolorbox}
  - |
    \let\oldtexttt\texttt
    \renewcommand{\texttt}[1]{\small\oldtexttt{#1}}
---

# Introduction

Welcome to the **Clara API** core documentation. This software development project is replacing database connectivity in FIONA and provides structured data capture for research data.
