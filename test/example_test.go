package test

import (
	"testing"
	"time"

	"github.com/keon94/go-compose/docker"

	"github.com/go-redis/redis/v7"
	"github.com/stretchr/testify/require"
)

func TestRedis(t *testing.T) {
	env := docker.StartEnvironment(
		&docker.EnvironmentConfig{
			UpTimeout:        30 * time.Second,
			DownTimeout:      30 * time.Second,
			ComposeFilePaths: []string{"docker-compose.tests.yml"},
		},
		&docker.ServiceEntry{
			Name:    "redis",
			Handler: GetRedisClient,
		},
	)
	t.Cleanup(env.Shutdown)
	client := env.Services["redis"].(*redis.Client)
	client.Set("key", "value", 0)
	cmd := client.Get("key")
	require.NoError(t, cmd.Err())
	require.Equal(t, "value", cmd.Val())
}
