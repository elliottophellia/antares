---
name: browser-automation
description: Drive a website reliably — logins, forms, search, and multi-page flows. Use when a task needs a real browser.
tags: [browser, web, automation]
triggers: [browser, click, log in, form, website, scrape]
---

# Driving a browser

## The loop

`navigate` → `snapshot` → act on a reference → `snapshot` again.

The snapshot lists what a person could act on, each with a reference like
`e7`. Those references are only valid until the page changes. After anything
that navigates, submits, or re-renders, take a fresh snapshot before acting
again — a stale reference will refuse rather than click the wrong thing.

## Reading a page

`snapshot` is for deciding what to do; `text` is for reading content. Use
`text` with a reference to pull just one region rather than the whole page.

`screenshot` writes a PNG to disk. It is only useful to a model that can see
images — otherwise prefer `text`.

## Forms

Type into each field by reference, then click the submit button. Use
`submit: true` on the last field only when the form has no visible button.

After submitting, wait for something specific with `wait_for` rather than
assuming the next page is ready.

## When things go wrong

- **Nothing interactive on the page** — the app may still be rendering. Use
  `wait_for` with text you expect, then snapshot again.
- **The click did nothing** — some widgets need the real element focused.
  Try `press` after clicking, or `eval` for a stubborn case.
- **Blocked or a captcha** — stop and say so. Do not attempt to defeat it.

## Housekeeping

The browser stays open between calls, which is what lets a login persist. Call
`close` when the task is done if you know nothing else needs it.
