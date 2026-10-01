package check

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/back-to-code/sante/internal/config"
)

func init() {
	// Both drivers also return every error they log; the logs would only
	// duplicate check results on stderr.
	_ = mysql.SetLogger(log.New(io.Discard, "", 0))
	redis.SetLogger(silentRedisLogger{})
}

type silentRedisLogger struct{}

func (silentRedisLogger) Printf(context.Context, string, ...any) {}

type tcpChecker struct{ address string }

func (c tcpChecker) Check(ctx context.Context) (string, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.address)
	if err != nil {
		return "", cleanError(err)
	}
	conn.Close()
	return "connected to " + conn.RemoteAddr().String(), nil
}

type redisChecker struct{ opts *redis.Options }

func newRedis(m config.Monitor) redisChecker {
	opts := &redis.Options{
		Addr:            m.Address,
		Username:        m.Username,
		Password:        m.Password,
		DB:              m.DB,
		MaxRetries:      -1,
		PoolSize:        1,
		DisableIdentity: true,
	}
	if m.TLS {
		host, _, _ := net.SplitHostPort(m.Address)
		opts.TLSConfig = &tls.Config{ServerName: host}
	}
	return redisChecker{opts: opts}
}

func (c redisChecker) Check(ctx context.Context) (string, error) {
	client := redis.NewClient(c.opts)
	defer client.Close()
	reply, err := client.Ping(ctx).Result()
	if err != nil {
		return "", cleanError(err)
	}
	return reply, nil
}

type mysqlChecker struct {
	cfg   *mysql.Config
	query string
}

func newMySQL(m config.Monitor) (mysqlChecker, error) {
	cfg, err := mysql.ParseDSN(m.DSN)
	if err != nil {
		return mysqlChecker{}, fmt.Errorf("dsn: %w", err)
	}
	return mysqlChecker{cfg: cfg, query: m.Query}, nil
}

func (c mysqlChecker) Check(ctx context.Context) (string, error) {
	connector, err := mysql.NewConnector(c.cfg)
	if err != nil {
		return "", err
	}
	db := sql.OpenDB(connector)
	defer db.Close()

	if c.query == "" {
		if err := db.PingContext(ctx); err != nil {
			return "", cleanError(err)
		}
		return "ping ok", nil
	}
	rows, err := db.QueryContext(ctx, c.query)
	if err != nil {
		return "", cleanError(err)
	}
	defer rows.Close()
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		return "", cleanError(err)
	}
	return "query ok", nil
}

type postgresChecker struct {
	cfg   *pgx.ConnConfig
	query string
}

func newPostgres(m config.Monitor) (postgresChecker, error) {
	cfg, err := pgx.ParseConfig(m.DSN)
	if err != nil {
		return postgresChecker{}, fmt.Errorf("dsn: %w", err)
	}
	return postgresChecker{cfg: cfg, query: m.Query}, nil
}

func (c postgresChecker) Check(ctx context.Context) (string, error) {
	conn, err := pgx.ConnectConfig(ctx, c.cfg)
	if err != nil {
		return "", cleanError(err)
	}
	defer conn.Close(context.WithoutCancel(ctx))

	if c.query == "" {
		if err := conn.Ping(ctx); err != nil {
			return "", cleanError(err)
		}
		return "ping ok", nil
	}
	rows, err := conn.Query(ctx, c.query)
	if err != nil {
		return "", cleanError(err)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", cleanError(err)
	}
	return "query ok", nil
}
