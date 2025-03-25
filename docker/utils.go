package docker

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
	"time"
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

func ColoredPrintf(color Color, msg string) {
	colored := string(color) + strings.ReplaceAll(msg, "\n", "\n"+string(color)) + string(ColorReset)
	fmt.Println(colored)
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

func PrintLogs(color Color, container *Container) {
	logs, err := container.Logs()
	if err != nil {
		logger.Errorf("Couldn't get logs for service=%s", container.Config.Names[0])
	} else {
		ColoredPrintf(color, fmt.Sprintf("============================%s logs============================\n", container.Config.Names[0]))
		ColoredPrintf(color, logs)
		ColoredPrintf(color, "===============================================================\n")
	}
}

func PrintContainerState(color Color, container *Container) {
	state, err := container.State()
	if err != nil {
		logger.Errorf("Couldn't get logs for service=%s", container.Config.Names[0])
	} else {
		ColoredPrintf(color, fmt.Sprintf("============================%s state============================\n", container.Config.Names[0]))
		ColoredPrintf(color, state)
		ColoredPrintf(color, "================================================================\n")
	}
}

func PrintMap(m *sync.Map) string {
	str := ""
	m.Range(func(key, value interface{}) bool {
		str += fmt.Sprintf("{%v -> %v},", key, value)
		return true
	})
	return str
}

func RunProcessWithLogs(cmd *exec.Cmd, logHandler func(msg string)) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	logger := func(pipe io.ReadCloser) {
		scanner := bufio.NewScanner(pipe)
		scanner.Split(bufio.ScanLines)
		for scanner.Scan() {
			m := scanner.Text()
			logHandler(m)
		}
	}
	go logger(stdout)
	go logger(stderr)
	return nil
}

func parsePorts(ports []container.Port) (map[int][]int, error) {
	portMap := make(map[int][]int)
	for _, port := range ports {
		key := int(port.PrivatePort)
		vals := portMap[key]
		portMap[key] = append(vals, int(port.PublicPort))
	}
	return portMap, nil
}

func runCommand(cmd *exec.Cmd, timeout ...time.Duration) error {
	if err := RunProcessWithLogs(cmd, func(msg string) {
		ColoredPrintf(GREEN, msg)
	}); err != nil {
		return err
	}
	if len(timeout) == 0 {
		return cmd.Wait()
	}
	waiter := time.After(timeout[0])
	done := make(chan error)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case <-waiter:
		return fmt.Errorf("process did not complete within the timeout\n%s", string(debug.Stack()))
	case err := <-done:
		if err != nil {
			return fmt.Errorf("process returned error: %w\n%s", err, string(debug.Stack()))
		}
		return nil
	}
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

type (
	Endpoints interface {
		GetHost() string
		GetPublicPorts(privatePorts ...int) []int
	}

	endpoints struct {
		host  string
		ports map[int][]int
	}
)

func (p *endpoints) GetHost() string {
	return p.host
}

func (p *endpoints) GetPublicPorts(privatePorts ...int) []int {
	var ports []int
	contains := func(p int) bool {
		for _, port := range privatePorts {
			if p == port {
				return true
			}
		}
		return false
	}
	for k, v := range p.ports {
		if len(privatePorts) == 0 || contains(k) {
			ports = append(ports, v...)
		}
	}
	return ports
}
