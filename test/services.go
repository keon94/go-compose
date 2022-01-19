package test

import (
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/keon94/go-compose/docker"

	"github.com/go-redis/redis/v7"
)

func GetRedisClient(container *docker.Container) (interface{}, error) {
	host, ports, err := container.GetAllEndpoints()
	if err != nil {
		return "", err
	}
	publicPorts := ports["6379"]
	if len(publicPorts) == 0 {
		return nil, fmt.Errorf("redis port not found")
	}
	var connString string
	var conn *redis.Client
	for _, publicPort := range publicPorts {
		connString = fmt.Sprintf("%s:%s", host, publicPort)
		conn = redis.NewClient(&redis.Options{Addr: connString})
		if err == nil {
			break
		}
		logrus.Infof("redis connection \"%s\" failed with %s. trying another if available.", connString, err.Error())
	}
	if connString == "" {
		return nil, fmt.Errorf("no valid redis connection could be establised")
	}
	return conn, nil
}
