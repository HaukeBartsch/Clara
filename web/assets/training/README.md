# Training plans

The **Training** page (`/training`, `Design/User_Interface_Design.md` §2.7, REQ-UI-059) shows one card per directory here. Adding or changing a plan needs no code change: edit the files and reload the page.

## One directory per plan

```
web/assets/training/
└── 03-collect-data/        the plan's id: lower-case letters, digits, - and _
    ├── plan.md             the text, in English (required — without it the directory is ignored)
    ├── plan.nb.md          optional: the same text in another UI language (nb, nn, …)
    ├── 01.png              the pictures, shown in file-name order;
    ├── 02.png              the first one is also the card's picture
    └── …
```

- Plans are listed in directory-name order, so a number prefix sets the order.
- Pictures may be PNG, JPEG, GIF or WebP. File names start with a letter or digit and use only letters, digits, `.`, `-` and `_`. A plan without pictures shows its text only.
- A user sees `plan.<language>.md` for their UI language when it exists, and `plan.md` otherwise. The five example plans ship in English plus Norwegian (`plan.nb.md`, `plan.nn.md`).
- A plan can be linked to directly: `/training#03-collect-data`.

A translation follows its `plan.md` structure step for step — the same blocks in the same order, one numbered step per picture — and changes only the words. Name the buttons and fields as the translated application names them (`tools/i18n/<language>.json`), so the text points at what the user sees.

## The text

`plan.md` is a small subset of Markdown. Everything else is shown as plain text — HTML is never interpreted.

```
# Title of the plan                 first line: the card's and the dialog's title

The first paragraph is the card's short text, and starts the dialog's text.

1. Numbered steps; a step's picture   lines starting with "1." form a numbered list,
   is easiest to follow when its      an indented line continues the step above
   number matches the file name.
- Bullet points                       lines starting with "-" form a bulleted list

## A heading                          headings inside the text

**bold** for the names of buttons and links, `code` for field names and patterns.
```

The example plans' pictures are screenshots of the development stack (`e2e/dev-stack.sh`), 1280 × 860 pixels, with the control to use outlined. `e2e/capture-training.mjs` re-captures plans 01–04 and `e2e/capture-training-05.mjs` plan 05; each wants a stack with a database of its own, so restart `dev-stack.sh` between the two runs.
