package test

import (
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/keon94/go-compose/docker"

	"github.com/go-redis/redis/v7"
)

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
		return nil, fmt.Errorf("no valid redis connection could be establised")
	}
	return conn, nil
}
