package test

import (
	"fmt"

	"github.com/keon94/go-compose/docker"

	"github.com/go-redis/redis/v7"
)

func GetRedisClient(container *docker.Container) (interface{}, error) {
	host, port, err := docker.GetEndpoint(container)
	if err != nil {
		return nil, err
	}
	return redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%s", host, port),
	}), nil
}
