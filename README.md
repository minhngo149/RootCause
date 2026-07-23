# RootCause

[![CI](https://github.com/minhngo149/RootCause/actions/workflows/ci.yml/badge.svg)](https://github.com/minhngo149/RootCause/actions/workflows/ci.yml)

> Find the root cause, not the symptoms.

RootCause is a CLI production-diagnosis tool. It runs deterministic rules
against your SQL, then explains **why** something is a problem using a
curated knowledge base — instead of just flagging it and moving on.

Rule First. Knowledge First. AI Second.

## Status

Early, pre-release (`v0.1.0-dev`, no tagged version yet). Currently supports
SQL text analysis with 2 rules (`SQL001` avoid `SELECT *`, `SQL002`
`UPDATE`/`DELETE` without `WHERE`) and 3 knowledge articles. See
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
```

Homebrew tap and standalone binary downloads are planned once there's a
tagged release — see
[docs/10-cli-release-plan.md](docs/10-cli-release-plan.md) (Giai đoạn 2).

## Usage

```bash
# Diagnose a single file (SQL text, slow-query log, EXPLAIN output)
rootcause doctor path/to/query.sql

# Recursively review every .sql file under a directory
rootcause review .

# Explain a concept a rule referenced
rootcause explain covering-index

# Browse the knowledge base
rootcause learn
```

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
