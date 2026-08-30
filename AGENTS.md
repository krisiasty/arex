# Working in this repository

Instructions for coding agents. Humans may find the reasoning useful too.

## Run golangci-lint before you call anything finished

```bash
golangci-lint run ./...   # must report 0 issues
golangci-lint fmt         # applies gofumpt
go test ./...
```

**gopls diagnostics are not sufficient on their own.** The gopls MCP servers run headless, where gopls has no way to
receive settings — no config file, no flag, and it reads no `GOPLS_*` variable that would carry them. So
`go_diagnostics` reports gopls defaults, which do **not** include `shadow` or `gofumpt`. Code that gopls calls clean
can still fail CI.

Use gopls for what it is good at — navigation, references, type errors, impact analysis before a rename — and
golangci-lint for whether the code is acceptable.

## What CI enforces beyond the defaults

| Check | Why it is here |
| --- | --- |
| `gofumpt` | gofmt plus the rules gofmt left out; formatting is never a review topic |
| `govet shadow` | an inner `err` hiding an outer one |

Where shadowing is deliberate — a closure that must not write to the enclosing `err`, a loop variable that must stay
per-iteration — use `//nolint:govet // shadow: <reason>`. `nolintlint` requires the reason, and a bare directive
fails the build.

`.golangci.yml` ends with a list of linters that were measured against this codebase and deliberately left off, with
the finding count and the reason for each. Read it before proposing to enable one: the answer may already be there.

## Tests

New behaviour gets a test that failed first. Fixtures in `testdata/` are real, anonymized `show ... | json` captures
from an Arista switch — `testdata/README.md` records what was preserved on purpose and why. Do not regenerate a
fixture to make a test pass.
