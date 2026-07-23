# RootCause

[![CI](https://github.com/minhngo149/RootCause/actions/workflows/ci.yml/badge.svg)](https://github.com/minhngo149/RootCause/actions/workflows/ci.yml)

> Find the root cause, not the symptoms.

RootCause is a CLI production-diagnosis tool. It runs deterministic rules
against your SQL, then explains **why** something is a problem using a
curated knowledge base — instead of just flagging it and moving on.

Rule First. Knowledge First. AI Second.

## Status

Early, pre-release (`v0.1.0-dev`, no tagged version yet). Currently supports
SQL and Go source analysis with 2 rules (`SQL001` avoid `SELECT *`,
`SQL002` `UPDATE`/`DELETE` without `WHERE`) and 3 knowledge articles. See
[docs/](docs/) for the full roadmap, risk assessment, and release plan.

## Install

```bash
go install github.com/minhngo149/RootCause/cmd/rootcause@latest
```

Or build from source:

```bash
git clone git@github.com:minhngo149/RootCause.git
cd RootCause
go build -o bin/rootcause ./cmd/rootcause
./bin/rootcause learn   # run it with ./bin/ — it isn't on your PATH yet
```

`go build -o bin/rootcause` only creates the binary at `bin/rootcause`; it does
not put it on your `PATH`, so a bare `rootcause` command will not be found.
Either keep invoking it as `./bin/rootcause`, or move/symlink it onto your
`PATH` (e.g. `sudo mv bin/rootcause /usr/local/bin/`). `go install` (above)
does not have this problem — it places the binary in `$(go env GOPATH)/bin`,
which is usually already on `PATH`.

Homebrew tap and standalone binary downloads are planned once there's a
tagged release — see
[docs/10-cli-release-plan.md](docs/10-cli-release-plan.md) (Giai đoạn 2).

## Usage

```bash
# Diagnose a single file: SQL text, a slow-query log/EXPLAIN dump, or a Go
# source file (queries embedded in db.Query/Exec/... calls are extracted)
rootcause doctor path/to/query.sql
rootcause doctor internal/store/user.go

# Review changed files: by default, only what's uncommitted or committed
# locally but not yet pushed to the branch's upstream (i.e. what a `git
# push` would send, plus your dirty working tree)
rootcause review .

# Review the entire repository instead of just changed files
rootcause review . --scan

# Explain a concept a rule referenced
rootcause explain covering-index

# Browse the knowledge base
rootcause learn
```

(Replace `rootcause` with `./bin/rootcause` in the commands above if you
built from source instead of using `go install`.)

`review` currently understands `.sql` files and `.go` files. Go support
extracts the first string-literal argument of calls to well-known database
methods (`Query`, `Exec`, `Prepare`, sqlx's `Get`/`Select`, gorm's `Raw`,
...) — see [internal/analyzer/golang.go](internal/analyzer/golang.go).
Queries built via string concatenation or passed in as a variable aren't
detected yet; this is a deliberate scope limit, not an oversight (real
type/data-flow analysis is a much larger effort — see the SQL dialect
fragmentation discussion in [docs/09-risks.md](docs/09-risks.md)). More
languages are added by registering a new extractor in
[internal/analyzer/analyzer.go](internal/analyzer/analyzer.go).

Example:

```
$ rootcause doctor query.sql
query.sql — 1 issue(s) found:

 MEDIUM  Avoid SELECT * (SQL001)
  line 1: SELECT * FROM orders WHERE customer_id = 42;
  Recommendation:
    - Specify only the columns your application actually needs.
    - Adding explicit columns lets the query use a covering index instead of falling back to a table lookup.
  Why: Execution Plan -> rootcause explain execution-plan
  Why: Covering Index -> rootcause explain covering-index
```

## Project structure

```
cmd/rootcause/        CLI entrypoint
internal/cli/          cobra commands (doctor, review, explain, learn)
internal/analyzer/     per-language query extraction (SQL, Go, ...)
internal/vcs/          git-based changed-file detection for `review`
internal/ruleengine/   deterministic rule loader + detectors
internal/knowledge/    knowledge base loader
internal/render/       terminal output (violations, markdown)
knowledge/             markdown knowledge base (embedded into the binary)
rules/                 YAML rule definitions (embedded into the binary)
docs/                  vision, architecture, risks, release plan
```

## Contributing a rule

Rules are plain YAML — no engine code changes needed. See an existing rule
in [rules/sql/](rules/sql/) for the shape, and [docs/09-risks.md](docs/09-risks.md)
for why new rules should ship conservatively (avoid false positives).

## License

TBD — see [docs/10-cli-release-plan.md](docs/10-cli-release-plan.md) Giai đoạn 0.
