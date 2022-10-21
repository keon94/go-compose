[![Circle-CI-Build](https://circleci.com/gh/keon94/go-compose.svg?style=shield)](https://app.circleci.com/pipelines/github/keon94/go-compose?branch=main)


# go-compose
An API in Go to wrap and manage docker-compose calls. It is intended for
writing integration tests against one or more docker-compose files.

Pull by running:
```go get github.com/keon94/go-compose```

Example usage:
```go

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
```

The *Handler* is a user-defined function that allows you to access the container API, typically so you can initialize a client. Following the example above, we can define *GetRedisClient* as such:

```go
func GetRedisClient(container *docker.Container) (interface{}, error) {
	endpoints, err := container.GetEndpoints()
	if err != nil {
		return "", err
	}
	var connString string
	var conn *redis.Client
	for _, publicPort := range endpoints.GetPublicPorts(6379) {
		connString = fmt.Sprintf("%s:%d", endpoints.GetHost(), publicPort)
		conn = redis.NewClient(&redis.Options{Addr: connString})
        err = conn.Ping().Err()
		if err == nil {
			break
		}
		logrus.Infof("redis connection \"%s\" failed with %s. trying another if available.", connString, err.Error())
		connString = ""
	}
	if connString == "" {
		return nil, fmt.Errorf("no valid redis connection could be established")
	}
	return conn, nil
}
```

The docker-compose content:
```yaml
version: "2.4"

services:
  redis:
    labels:
      - "integration"
    networks:
      - "tests"
    image: redis:5.0.8-alpine
    volumes:
      - redis-volume-test:/data
    ports:
      - "6379"

networks:
  tests:
    name: "tests"

volumes:
  redis-volume-test:
```

Notes:
* In the example above, the map ```env.Services["redis"].(*redis.Client)``` returns the client returned by the *Handler* function, so you need to ensure you're casting it to the correct type.
* The services in the docker-compose file are expected to use a specific label and network, which default to "integration" and "tests" respectively. You can change these by configuring the ```EnvironmentConfig``` and ```ServiceEntry``` objects accordingly.

See [these tests](test/) for concrete examples.
