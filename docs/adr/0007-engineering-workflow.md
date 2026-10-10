# ADR-0007: Engineering workflow for a protocol-reverse-engineered, single-maintainer project

## Status
Proposed (2026-10-10). Addresses KI-15 and the process gaps behind KI-01/KI-02.
Implemented so far: CI (`ci.yml`, PR #7 and #8), sandbox e2e harness and workflow
(PR #9, KI-34).

## Context
- CI has never run on GitHub (`.github/Workflows` instead of `.github/workflows`), and
  `main` has a failing test.
- Changes landed directly on `main`, validated by running shell scripts against a live
  account.
- The highest-impact bugs (crashes, wrong Today) came from untested assumptions about
  what the official client does, not from Go-level mistakes.
- Most code will be written by AI agents (Claude Code, Codex) supervised by one
  maintainer.

## Decision

### Branching and review
- `main` is protected and always green. Work happens on short-lived branches named
  `ki-NN-short-slug` or `feat-short-slug`; merge by PR.
- PR description in Russian (maintainer convention), with a checklist: tests added,
  docs updated, KI/ADR referenced, wire-format evidence attached if a write changed.

### Test pyramid
| Layer | What | Where | Runs |
|---|---|---|---|
| Unit | pure domain: civil dates, classifier, encoders, parsers | `domain/...` | every push |
| Tape | SDK HTTP behaviour against recorded responses | `tapes/`, `*_test.go` | every push |
| Capture | our payloads vs official-client captures | `testdata/captures/` | every push |
| Conformance | classifier vs real Things DB snapshots | `testdata/conformance/` | every push |
| Sandbox e2e | full MCP round trip on a dedicated Things account | `server/sandbox_*_test.go` (tag `e2e`, `docs/testing/sandbox-e2e.md`); `tests/*.sh` for a deployed server | before merging write-path changes; at most daily |
| Prod smoke | read-only health of the deployed server | `GET /readyz`, `things_list(view=today)` | after deploy |

Rules:
- New logic → new test in the same package. Bug fix → failing test first, named after
  the KI (`TestX_KI02_...`).
- Tests never touch the owner's real account. The sandbox account credentials live in
  CI secrets / a local `.env.sandbox` (git-ignored) and are only used by e2e jobs.
- No test depends on wall-clock time; inject the clock.

### CI (GitHub Actions)
- Move workflows to `.github/workflows/`; delete the nested duplicate and Fly.io deploy.
- Jobs: `go build -o /dev/null ./...` (plain `go build ./...` collides with the
  `server/` directory), `go vet ./...`, `go test -race ./...`, `staticcheck`,
  `govulncheck`, `go mod tidy` diff check.
- Go version from `go.mod` (`go-version-file: go.mod`).
- Triggers: `pull_request` and `push` to `main`, never `pull_request_target`. Workflow
  permissions `contents: read`; checkout without persisted credentials. The check
  workflow uses no secrets, deploys nothing and never reaches Things Cloud; sandbox e2e
  jobs get a workflow of their own.
- Actions are pinned to a full commit SHA with the release in a comment
  (`actions/checkout@<sha> # v7.0.1`).
- Release: on tag `v*`, build a multi-arch image and push to GHCR; the VPS pulls a
  pinned tag (ADR-0002).
- Status (2026-10-11): `.github/workflows/ci.yml` runs the tidy check, build, vet and
  race tests, and `govulncheck` in a separate job, on the Go `toolchain` from `go.mod`
  (KI-32). `staticcheck` waits for KI-33; release and Dependabot are not set up.

### Agent workflow
- `AGENTS.md` is the contract for every agent; `CLAUDE.md` imports it.
- Agents must: read the relevant KI/ADR first, keep changes scoped to one KI, run
  `go build`, `go vet`, `go test ./...` before claiming done, update docs touched by the
  change, and state what they could not verify (e.g. behaviour on a real Things client).
- Architectural changes start as an ADR (status Proposed) reviewed by the maintainer.

### Knowledge management
- `docs/known-issues.md` is the live defect register.
- `docs/protocol/things-cloud.md` is the live protocol reference with evidence levels.
- Dated investigation notes (`docs/YYYY-MM-DD-*.md`) are historical records; new
  investigations follow the same naming and end with links to the KI/ADR they produced.

### Dependencies
- Renovate or Dependabot for Go modules and GitHub Actions, weekly, grouped.
- Upgrade `mcp-go` deliberately: it is the transport layer and has had breaking
  changes; pin and test with the protocol test script.

## Consequences
- Easier: regressions are caught before they reach the owner's Things; agents get a
  clear definition of done.
- Harder: a sandbox Things account must be maintained; capture sessions cost time.
