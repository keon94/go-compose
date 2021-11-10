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

func TestRedis_ManualStartStop1(t *testing.T) {
	env := docker.StartEnvironment(
		&docker.EnvironmentConfig{
			UpTimeout:        30 * time.Second,
			DownTimeout:      30 * time.Second,
			ComposeFilePaths: []string{"docker-compose.tests.yml"},
		},
	)
	t.Cleanup(env.Shutdown)
	require.Nil(t, env.Services["redis"])
	// try stopping non-running service
	err := env.StopServices("redis")
	require.Error(t, err)
	// start the service
	err = env.StartServices(&docker.ServiceEntry{
		Name:    "redis",
		Handler: GetRedisClient,
	})
	require.NoError(t, err)
	client := env.Services["redis"].(*redis.Client)
	client.Set("key", "value", 0)
	cmd := client.Get("key")
	require.NoError(t, cmd.Err())
	require.Equal(t, "value", cmd.Val())
	// stop the service
	err = env.StopServices("redis")
	require.NoError(t, err)
	// call client on stopped service
	status := client.Set("key2", "value", 0)
	require.Error(t, status.Err())
}

func TestRedis_ManualStartStop2(t *testing.T) {
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
	require.NotNil(t, env.Services["redis"].(*redis.Client))
	// try stopping non-running service
	require.NoError(t, env.StopServices("redis"))
	// double stop -> ok
	require.NoError(t, env.StopServices("redis"))
	// try stopping some fake service -> error
	require.Error(t, env.StopServices("fake"))
	// start the service
	err := env.StartServices(&docker.ServiceEntry{
		Name:    "redis",
		Handler: GetRedisClient,
	})
	require.NoError(t, err)
	client := env.Services["redis"].(*redis.Client)
	client.Set("key", "value", 0)
	cmd := client.Get("key")
	require.NoError(t, cmd.Err())
	require.Equal(t, "value", cmd.Val())
	// double start service -> no-op, but new client instance (handler runs a second time)
	err = env.StartServices(&docker.ServiceEntry{
		Name:    "redis",
		Handler: GetRedisClient,
	})
	require.NoError(t, err)
	client2 := env.Services["redis"].(*redis.Client)
	require.NotSame(t, client, client2)
}
