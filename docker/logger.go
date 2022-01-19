package docker

import "github.com/sirupsen/logrus"

var logger = logrus.New()

func init() {
	logger.SetFormatter(&logrus.TextFormatter{
		ForceColors:   true,
		FullTimestamp: true,
		PadLevelText:  true,
	})
}
