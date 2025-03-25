package docker

import "github.com/sirupsen/logrus"

var logger = logrus.New()

type Color string

const (
	RED        = Color("\033[31m")
	GREEN      = Color("\033[32m")
	YELLOW     = Color("\033[33m")
	BLUE       = Color("\033[34m")
	MAGENTA    = Color("\033[35m")
	CYAN       = Color("\033[36m")
	WHITE      = Color("\033[37m")
	ColorReset = Color("\033[0m")
)

func init() {
	logger.SetFormatter(&logrus.TextFormatter{
		ForceColors:   true,
		FullTimestamp: true,
		PadLevelText:  true,
	})
}
