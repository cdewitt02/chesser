# The Go implementation

Superseded by the Python tree at the repository root
([ADR 0002](../docs/adr/0002-python-rewrite.md)). Kept for one release so the
cutover stays revertible, and so the Phase 0 goldens can be regenerated from the
implementation that produced them.

**Nothing here is built or tested by CI, and nothing outside this directory
imports it.** It is a reference, not a fallback: the Python tree owns the
database, and running both against one corpus is only safe because the schema is
untouched.

## What it is still good for

`cmd/golden` is the capture tool for `testdata/golden/`. Regenerating from here
is what makes those files a *reference* rather than a self-portrait:

```sh
cd legacy && . ../.env && go run ./cmd/golden cdew4
```

The paths inside it are relative to the repository root, so run it from `legacy/`
and it writes to `../testdata/golden/` — see `testdata/golden/MANIFEST.md`.

## When this goes away

Delete it once the Python tree has run a real ingestion cycle and a few chat
sessions without surprises. At that point the goldens stop being a cross-language
reference and become a Python-vs-Python regression suite frozen at the cutover,
which is still worth having and is stated as such in the manifest.
