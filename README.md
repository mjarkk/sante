# Sante

A small, self-hosted uptime monitor for microservices. Sante checks HTTP
endpoints, TCP ports, Redis, MySQL and PostgreSQL on a schedule you choose,
stores every result in SQLite for 90 days in a Material 3 Expressive design.

![The status page of the Docker Compose demo during a partial outage](docs/status-page.png)

- **One YAML file** [configures everything](docs/configuration.md).
- **Status page** with 90 daily bars per monitor, grouped services, an
  overall banner, tooltips, auto-refresh, light/dark themes.

## Quick start

### Docker Compose demo

```sh
docker compose up --build
```

This starts Sante on <http://localhost:8080> together with throwaway Redis,
PostgreSQL and MySQL containers, so every check type in
[`config.example.yaml`](config.example.yaml) has a live target. One monitor,
Payments API, points at a closed port so there is always an outage to look
at. It runs with `-seed`, so the bars show 90 days of
[synthetic history](#demo-data) right away. Use `SANTE_PORT=9090 docker compose up` if port 8080 is taken. The access token
is `sante`; set `SANTE_TOKEN` to change it.

### Docker

```sh
docker build -t sante .
docker run -d --name sante -p 8080:8080 \
  -v $PWD/config.yaml:/etc/sante/config.yaml:ro \
  -v sante-data:/data \
  -e TZ=Europe/Amsterdam -e DB_PASSWORD=... \
  sante
```

The image runs as a non-root user, stores its database in the `/data` volume,
and has a `HEALTHCHECK` that runs `sante healthcheck` against `/health`, using
`server.auth_token`. The bundled example config refuses to start until
`SANTE_TOKEN` is set.

### From source

```sh
cp config.example.yaml config.yaml   # then edit
go run . -config config.yaml
```

Requires Go 1.27.

## Commands

```
sante [serve] [-seed]   run the monitor and web UI (default)
sante seed [-reset]     fill the database with 90 days of synthetic history
sante validate          check the config file, print the monitors, and exit
sante healthcheck       exit 0 when the local instance reports healthy
sante version           print the version
```

All commands accept `-config path`. The default is `$SANTE_CONFIG`, then
`config.yaml`.

## Configuration

See [docs/configuration.md](docs/configuration.md) for every setting,
environment variable expansion and the fields of each monitor type, and
[`config.example.yaml`](config.example.yaml) for a commented example.

## Demo data

To see a populated status page without waiting 90 days, seed the database
with synthetic history for the monitors in your config:

```sh
sante serve -seed -config config.yaml     # seed on startup if the database is empty
sante seed -config config.yaml            # seed once and exit
sante seed -reset -config config.yaml     # replace existing history of the configured monitors
```

With Docker: `docker run ... sante serve -seed`, or
`docker compose run --rm sante seed -reset`.

The generator writes one result per monitor every interval (at least every
5 minutes) for the past 90 days. It uses type-appropriate latencies and error
messages, a daily latency rhythm, a few incidents of varying length, and the
odd isolated failure. Results go through the same rollup path as real checks,
so every page shows them as it would real data. The pattern is derived from
each monitor's id, so seeding again produces the same history.

`serve -seed` never touches a database that already holds results, so it is
safe to leave on across restarts. `seed` refuses to write into a non-empty
database unless you pass `-reset`, which deletes the configured monitors'
history first. Real checks carry on from the moment Sante starts.
