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

| Key           | Required | Meaning                                                          |
| ------------- | -------- | ---------------------------------------------------------------- |
| `name`        | yes      | Display name                                                     |
| `type`        | yes      | `http`, `tcp`, `redis`, `mysql`, `postgres` or `health_endpoint` |
| `id`          |          | Key for its history and URL; defaults to a slug of `name`        |
| `group`       |          | Monitors with the same group are listed together                 |
| `description` |          | Subtitle on the status and monitor pages                         |
| `interval`    |          | Overrides `defaults.interval`                                    |
| `timeout`     |          | Overrides `defaults.timeout`; can't be longer than `interval`    |
| `retries`     |          | Overrides `defaults.retries`                                     |
| `monitors`    |          | Sub-monitors, see below                                          |

History is stored by `id`, so renaming a monitor that has no `id` starts its
history over.

### Sub-monitors

A monitor can list other monitors under `monitors`, for checks that belong to
it, such as a second endpoint of the same app. On the status page each one is
a compact row inside its parent's card instead of a card of its own. In every
other way it's a normal monitor, with its own checks, history, page and
alerts.

```yaml
- name: App (next)
  group: App
  type: http
  url: https://next.example.com
  monitors:
    - name: Matcher
      type: http
      url: https://next.example.com/matcher/health
```

- A sub-monitor takes every monitor field except `group`, which is always its
  parent's, and `monitors`: they only go one level deep.
- `interval`, `timeout` and `retries` come from `defaults`, not from the
  parent.
- Without an `id`, the parent's id goes in front of the name: `app-next-matcher`
  here. If you move an existing monitor under a parent, set `id` to its old
  id to keep its history.
- The card's colour follows the parent's own check. A sub-monitor that is down
  turns only its own row red.
- Alerts, the monitor page and the health page name the parent too: "App
  (next) › Matcher".

Fields per type:

| Type              | Fields                                                                                                                           | Healthy when                                           |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------ |
| `http`            | `url`, `method` (GET), `headers`, `body`, `expect_status` (200–399), `expect_body`, `follow_redirects` (true), `tls_skip_verify` | the status matches and the body contains `expect_body` |
| `tcp`             | `address` (`host:port`)                                                                                                          | a connection opens                                     |
| `redis`           | `address`, `username`, `password`, `db`, `tls`                                                                                   | `PING` succeeds                                        |
| `mysql`           | `dsn` ([go-sql-driver format](https://github.com/go-sql-driver/mysql#dsn-data-source-name)), `query`                             | it connects and `query` (default: ping) succeeds       |
| `postgres`        | `dsn` (URL or `key=value`), `query`                                                                                              | it connects and `query` (default: ping) succeeds       |
| `health_endpoint` | `token`, `method` (GET or POST, default GET)                                                                                     | the service called within the last `interval`          |

Every check opens a new connection. Nothing is pooled.

### Health endpoints

A `health_endpoint` monitor works the other way round: your service or cron
job calls Sante, and the monitor goes down when the calls stop. Until the
first call it stays pending and never fails.

```yaml
- name: Nightly backup
  type: health_endpoint
  token: ${BACKUP_TOKEN}   # openssl rand -hex 32
  method: POST             # GET (default) or POST
  interval: 25h            # longest allowed gap between calls
```

The monitor's page shows the URL to call, `https://<sante host>/api/push/<token>`.
Only signed-in users can open that page. The health page and `sante validate`
hide the token. A request with the configured method counts as a call, and
its body is ignored. Other methods get `405`, unknown tokens `404`.

- Each call marks the monitor up right away. After the first call, the
  monitor goes down as soon as `interval` passes without another one. Set
  `interval` a little longer than how often the service calls, so a call
  that is slightly late doesn't count as missed.
- `timeout` isn't allowed, because `interval` already sets how late a call
  may be.
- The token is the only thing that authenticates a call, so make it long and
  random. Each monitor needs its own. Allowed characters are letters, digits,
  `.`, `_`, `~` and `-`.
- Sante keeps calls in memory, so a restart starts over: the monitor keeps
  its last state, unchecked, until the next call comes in.
- The URL uses the host you opened the page on. If the service reaches Sante
  at another address, use that host instead.

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
- `sante test-webhooks` sends a test alert to every webhook and prints which
  ones failed and why.
- Slack app webhooks ignore `channel` and `username`.
- In Mattermost, `channel` is the name from the channel's URL (`ops-alerts`),
  not its display name. A webhook locked to one channel can't post to others.
  `username` only works if *Enable integrations to override usernames* is on
  in the System Console. Mattermost alerts are plain text with no
  attachment, because Mattermost's search skips attachments.
- Webhook URLs contain a secret. Logs, `sante validate` and the health page
  show only the host.
