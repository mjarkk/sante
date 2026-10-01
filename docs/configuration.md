# Configuration

See [`config.example.yaml`](../config.example.yaml) for a commented example of
every option.

## Environment variables

Expansion runs on values after YAML parsing, so a substituted password
containing `#` or `: ` cannot break the file's structure.

| Syntax            | Result                                            |
| ----------------- | ------------------------------------------------- |
| `$VAR`, `${VAR}`  | value of `VAR`; empty (with a warning) when unset |
| `${VAR:-default}` | `default` when `VAR` is unset or empty            |
| `${VAR-default}`  | `default` when `VAR` is unset                     |
| `${VAR:?message}` | refuse to start when `VAR` is unset or empty      |
| `${VAR?message}`  | refuse to start when `VAR` is unset               |
| `$$`              | a literal `$`                                     |

Defaults can nest (`${PRIMARY:-${FALLBACK}}`). Unknown keys are rejected with
their line number, so a typo like `intervall:` fails loudly.

## Top-level settings

| Key                 | Default         | Meaning                                                   |
| ------------------- | --------------- | --------------------------------------------------------- |
| `server.listen`     | `:8080`         | HTTP listen address                                       |
| `server.timezone`   | `UTC`           | IANA zone that defines the calendar days of the bars      |
| `server.log_level`  | `info`          | `debug` logs every check; `info` logs state changes       |
| `server.auth_token` | required        | Access token that unlocks `/health` and the monitor pages |
| `storage.path`      | `data/sante.db` | SQLite file; its directory is created if needed           |
| `ui.title`          | `Sante`         | Name shown in the header and the browser tab              |
| `ui.refresh`        | `30s`           | How often open pages refresh themselves (minimum 5s)      |
| `defaults.interval` | `60s`           | Time between checks (minimum 1s)                          |
| `defaults.timeout`  | `10s`           | Per-attempt timeout, capped at the monitor's interval     |
| `defaults.retries`  | `0`             | Extra attempts, 1s apart, before a check counts as failed |

Durations use Go syntax (`30s`, `5m`, `1h30m`). A bare number means seconds.

## Monitors

Every monitor has these fields:

| Key           | Required | Meaning                                                           |
| ------------- | -------- | ----------------------------------------------------------------- |
| `name`        | yes      | Display name                                                      |
| `type`        | yes      | `http`, `tcp`, `redis`, `mysql` or `postgres`                     |
| `id`          |          | Stable key for its history and URL; defaults to a slug of `name`  |
| `group`       |          | Monitors that share a group are listed together under its heading |
| `description` |          | Subtitle on the status and monitor pages                          |
| `interval`    |          | Overrides `defaults.interval`                                     |
| `timeout`     |          | Overrides `defaults.timeout`; must not exceed `interval`          |
| `retries`     |          | Overrides `defaults.retries`                                      |

History is stored per `id`. Renaming a monitor without setting `id` starts a
fresh history.

Type-specific fields:

| Type       | Fields                                                                                                                           | Healthy when                                           |
| ---------- | -------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------ |
| `http`     | `url`, `method` (GET), `headers`, `body`, `expect_status` (200–399), `expect_body`, `follow_redirects` (true), `tls_skip_verify` | the status matches and the body contains `expect_body` |
| `tcp`      | `address` (`host:port`)                                                                                                          | a connection opens                                     |
| `redis`    | `address`, `username`, `password`, `db`, `tls`                                                                                   | `PING` succeeds                                        |
| `mysql`    | `dsn` ([go-sql-driver format](https://github.com/go-sql-driver/mysql#dsn-data-source-name)), `query`                             | the connection and `query` (default: ping) succeed     |
| `postgres` | `dsn` (URL or `key=value`), `query`                                                                                              | the connection and `query` (default: ping) succeed     |

Each check opens a fresh connection, so a pooled connection can never hide a
target that has stopped accepting new ones.
