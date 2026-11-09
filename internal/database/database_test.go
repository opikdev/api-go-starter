package database

import (
	"context"
	"testing"
	"time"
)

func TestOpenSizesThePool(t *testing.T) {
	const base = "postgres://app:secret@127.0.0.1:1/db"
	cases := map[string]struct {
		url     string
		poolMax int32
		want    int32
	}{
		"defaults to 10":                    {base, 0, 10},
		"keeps pool_max_conns from the URL": {base + "?pool_max_conns=3", 0, 3},
		"lets DATABASE_POOL_MAX win":        {base + "?pool_max_conns=3", 7, 7},
		"applies DATABASE_POOL_MAX alone":   {base, 25, 25},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			pool, err := Open(context.Background(), c.url, 5000, c.poolMax)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if got := pool.Config().MaxConns; got != c.want {
				t.Fatalf("MaxConns = %d, want %d", got, c.want)
			}
		})
	}
}

func TestOpenKeepsTheURLConnectTimeout(t *testing.T) {
	for url, want := range map[string]time.Duration{
		"postgres://app@127.0.0.1:1/db":                   5 * time.Second,
		"postgres://app@127.0.0.1:1/db?connect_timeout=2": 2 * time.Second,
	} {
		pool, err := Open(context.Background(), url, 5000, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := pool.Config().ConnConfig.ConnectTimeout; got != want {
			t.Errorf("%s: ConnectTimeout = %s, want %s", url, got, want)
		}
		pool.Close()
	}
}
