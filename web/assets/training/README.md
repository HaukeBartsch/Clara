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
- A user sees `plan.<language>.md` for their UI language when it exists, and `plan.md` otherwise.
- A plan can be linked to directly: `/training#03-collect-data`.

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

The example plans' pictures are screenshots of the development stack (`e2e/dev-stack.sh`), 1280 × 760 pixels, with the control to use outlined.
