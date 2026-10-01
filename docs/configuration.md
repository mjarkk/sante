# Configuration

Sante reads a single YAML file. [`config.example.yaml`](../config.example.yaml)
shows every option. Unknown keys are rejected with their line number.

## Environment variables

Any value can use environment variables:

| Syntax            | Result                                        |
| ----------------- | --------------------------------------------- |
| `$VAR`, `${VAR}`  | value of `VAR`; empty with a warning if unset |
| `${VAR:-default}` | `default` if `VAR` is unset or empty          |
| `${VAR-default}`  | `default` if `VAR` is unset                   |
| `${VAR:?message}` | refuse to start if `VAR` is unset or empty    |
| `${VAR?message}`  | refuse to start if `VAR` is unset             |
| `$$`              | a literal `$`                                 |

Defaults can nest: `${PRIMARY:-${FALLBACK}}`. Variables are filled in after the
YAML is parsed, so a password containing `#` or `: ` doesn't need quoting.

## Settings

| Key                 | Default         | Meaning                                                   |
| ------------------- | --------------- | --------------------------------------------------------- |
| `server.listen`     | `:8080`         | HTTP listen address                                       |
| `server.timezone`   | `UTC`           | IANA time zone that decides where each day's bar starts   |
| `server.log_level`  | `info`          | `debug` logs every check; `info` logs only state changes  |
| `server.auth_token` | required        | Token that unlocks `/health` and the monitor pages        |
| `storage.path`      | `data/sante.db` | SQLite file; its directory is created if needed           |
| `ui.title`          | `Sante`         | Name in the header and browser tab                        |
| `ui.refresh`        | `30s`           | How often open pages reload (minimum 5s)                  |
| `defaults.interval` | `60s`           | Time between checks (minimum 1s)                          |
| `defaults.timeout`  | `10s`           | Timeout per attempt, capped at the monitor's interval     |
| `defaults.retries`  | `0`             | Extra attempts, 1s apart, before a check counts as failed |

Durations look like `30s`, `5m` or `1h30m`. A bare number means seconds.

## Monitors

Fields every monitor has:

| Key           | Required | Meaning                                                       |
| ------------- | -------- | ------------------------------------------------------------- |
| `name`        | yes      | Display name                                                  |
| `type`        | yes      | `http`, `tcp`, `redis`, `mysql` or `postgres`                 |
| `id`          |          | Key for its history and URL; defaults to a slug of `name`     |
| `group`       |          | Monitors with the same group are listed together              |
| `description` |          | Subtitle on the status and monitor pages                      |
| `interval`    |          | Overrides `defaults.interval`                                 |
| `timeout`     |          | Overrides `defaults.timeout`; can't be longer than `interval` |
| `retries`     |          | Overrides `defaults.retries`                                  |

History is stored by `id`, so renaming a monitor that has no `id` starts its
history over.

Fields per type:

| Type       | Fields                                                                                                                           | Healthy when                                           |
| ---------- | -------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------ |
| `http`     | `url`, `method` (GET), `headers`, `body`, `expect_status` (200–399), `expect_body`, `follow_redirects` (true), `tls_skip_verify` | the status matches and the body contains `expect_body` |
| `tcp`      | `address` (`host:port`)                                                                                                          | a connection opens                                     |
| `redis`    | `address`, `username`, `password`, `db`, `tls`                                                                                   | `PING` succeeds                                        |
| `mysql`    | `dsn` ([go-sql-driver format](https://github.com/go-sql-driver/mysql#dsn-data-source-name)), `query`                             | it connects and `query` (default: ping) succeeds       |
| `postgres` | `dsn` (URL or `key=value`), `query`                                                                                              | it connects and `query` (default: ping) succeeds       |

Every check opens a new connection. Nothing is pooled.

## Notifications

When a monitor goes down, Sante posts to every webhook under
`notifications.webhooks`:

```yaml
notifications:
  webhooks:
    - type: slack
      url: ${SLACK_WEBHOOK_URL}
    - type: mattermost
      url: ${MATTERMOST_WEBHOOK_URL}
      channel: ops-alerts
      username: Sante
```

| Key        | Required | Meaning                                                   |
| ---------- | -------- | --------------------------------------------------------- |
| `type`     | yes      | `slack` or `mattermost`                                   |
| `url`      | yes      | Incoming webhook URL                                      |
| `channel`  |          | Channel name or `@user` to post to instead of the default |
| `username` |          | Name to post as instead of the webhook's                  |

- You get one message per outage, even if Sante restarts while the monitor is
  down. A monitor whose first check fails counts as down. There's no message
  when it recovers.
- A post is tried up to 3 times on network errors, `429` and `5xx`, and Sante
  waits as long as `Retry-After` says. If all attempts fail, the error is
  logged as `sending down notification`. Mattermost only says why in
  developer mode, but a `404` from it means the channel wasn't found.
- Slack app webhooks ignore `channel` and `username`.
- In Mattermost, `channel` is the name from the channel's URL (`ops-alerts`),
  not its display name. A webhook locked to one channel can't post to others.
  `username` only works if *Enable integrations to override usernames* is on
  in the System Console.
- Webhook URLs contain a secret. Logs, `sante validate` and the health page
  show only the host.
