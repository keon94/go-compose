package docker

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"

	"github.com/sirupsen/logrus"
)

func FindOpenTcpPort() (string, error) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	return fmt.Sprintf("%d", port), nil
}

func GetEndpoint(container *Container) (string, string, error) {
	host, ports, err := GetAllEndpoints(container)
	if err != nil {
		return "", "", err
	}
	var port string
	count := 0
	for _, publicPort := range ports {
		if count > 1 {
			return "", "", errors.New("multiple port bindings found")
		}
		port = publicPort
		count++
	}
	logrus.Printf("container: %s is running on host: %s, port: %s", container.Config.Names[0], host, port)
	return host, port, nil
}

func GetAllEndpoints(container *Container, internalPorts ...string) (string, map[string]string, error) {
	network := container.Config.NetworkSettings.Networks[Network]
	if network == nil {
		return "", nil, fmt.Errorf("network not found for container %s", container.Config.Names[0])
	}
	if len(container.Config.Ports) == 0 {
		return "", nil, fmt.Errorf("no ports found for container %s", container.Config.Names[0])
	}
	portMap, err := parsePorts(container.Config.Ports, internalPorts...)
	if err != nil {
		return "", nil, fmt.Errorf("error parsing ports for container %s", container.Config.Names[0])
	}
	host := "127.0.0.1"
	if runtime.GOOS == "linux" && !isWSL() {
		host = network.Gateway
	}
	logrus.Printf("container: %s is running on host: %s, port-bindings: %v", container.Config.Names[0], host, container.Config.Ports)
	return host, portMap, nil
}

func isWSL() bool {
	lines, err := ReadFile("/proc/version")
	if err != nil {
		return false
	}
	for _, line := range lines {
		l := strings.ToLower(line)
		if strings.Contains(l, "microsoft") {
			return true
		}
	}
	return false
}

func AwaitUntil(duration time.Duration, resolution time.Duration, f func() error) error {
	var err error
	for timeout := time.After(duration); true; {
		err = f()
		if err == nil {
			return nil
		}
		select {
		case <-timeout:
			return fmt.Errorf("timed out waiting. %v", err)
		default:
			time.Sleep(resolution)
		}
	}
	return err
}

func ReadFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func ReadEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	props := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		text := scanner.Text()
		split := strings.Split(text, "=")
		if len(split) != 2 {
			return nil, errors.New("Bad line: " + text)
		}
		props[split[0]] = split[1]
	}
	return props, scanner.Err()

}

// ColoredPrintf Yellow only
func ColoredPrintf(msg string) {
	colored := "\033[1;33m%s\033[0m" //yellow
	fmt.Printf(colored, msg)
}

// IsEmpty for whatever reason they don't like to add a simple Size()/Length() method to this...
func IsEmpty(m *sync.Map) bool {
	empty := true
	m.Range(func(_, _ interface{}) bool {
		empty = false
		return false
	})
	return empty
}

func PrintLogs(container *Container) {
	logs, err := container.Logs()
	if err != nil {
		logrus.Errorf("Couldn't get logs for service=%s", container.Config.Names[0])
	} else {
		ColoredPrintf(fmt.Sprintf("============================%s logs============================\n", container.Config.Names[0]))
		fmt.Printf("%s\n", logs)
	}
}

func Debug(i interface{}) {
	fmt.Println("=============Debug info:============")
	b, _ := json.Marshal(i)
	fmt.Println(string(b))
	fmt.Println("=============End of debug info============")
}

func PrintMap(m *sync.Map) string {
	str := ""
	m.Range(func(key, value interface{}) bool {
		str += fmt.Sprintf("{%v -> %v},", key, value)
		return true
	})
	return str
}

func parsePorts(ports []types.Port, privatePorts ...string) (map[string]string, error) {
	portMap := make(map[string]string)
	for _, port := range ports {
		if len(privatePorts) != 0 {
			for _, privatePort := range privatePorts {
				privatePortInt, err := strconv.ParseInt(privatePort, 10, 16)
				if err != nil {
					return nil, err
				}
				if port.PrivatePort == uint16(privatePortInt) {
					portMap[strconv.Itoa(int(port.PrivatePort))] = strconv.Itoa(int(port.PublicPort))
				}
			}
		} else {
			portMap[strconv.Itoa(int(port.PrivatePort))] = strconv.Itoa(int(port.PublicPort))
		}
	}
	return portMap, nil
}

type ContainerStatusCode uint8

const (
	Error ContainerStatusCode = iota
	Running
	Exited
	Unhealthy
	NotReady
)

type ContainerStatus struct {
	Error error
	Code  ContainerStatusCode
}
