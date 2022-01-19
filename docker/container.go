package docker

import (
	"context"
	"errors"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
	"io"
	"runtime"
	"strings"
)

type (
	// Container wrapped API for docker containers
	Container struct {
		cli           *client.Client
		Config        *types.Container
		ServiceConfig *ServiceConfig
	}
)

func (c *Container) GetStatus() *ContainerStatus {
	inspection, err := c.cli.ContainerInspect(context.Background(), c.Config.ID)
	if err != nil {
		return &ContainerStatus{
			Code:  Error,
			Error: err,
		}
	}
	if !inspection.State.Running {
		if inspection.State.ExitCode != 0 {
			return &ContainerStatus{
				Code: Error,
				Error: fmt.Errorf("container %s exited with error code %d. details: %s",
					c.Config.Names[0], inspection.State.ExitCode, inspection.State.Error),
			}
		}
		if strings.ToLower(inspection.State.Status) == "exited" {
			return &ContainerStatus{
				Code: Exited,
			}
		}
		return &ContainerStatus{
			Code: NotReady,
		}
	}
	if inspection.State.Health == nil { // health-check not supported
		return &ContainerStatus{
			Code: Running,
		}
	}
	if strings.ToLower(inspection.State.Health.Status) == "healthy" {
		return &ContainerStatus{
			Code: Running,
		}
	} else if strings.ToLower(inspection.State.Health.Status) != "unhealthy" {
		return &ContainerStatus{
			Code: NotReady,
		}
	}
	checks := inspection.State.Health.Log
	check := checks[len(checks)-1]
	return &ContainerStatus{
		Code: Unhealthy,
		Error: fmt.Errorf("unhealthy status for container %s. exit code: %d, health-check output: %s",
			c.Config.Names[0], check.ExitCode, check.Output),
	}
}

func (c *Container) Logs() (string, error) {
	out, err := c.cli.ContainerLogs(context.Background(), c.Config.ID, types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return "", err
	}
	buf := new(strings.Builder)
	_, err = io.Copy(buf, out)
	return buf.String(), nil
}

func (c *Container) Exec(cmd string) ([]string, error) {
	ctx := context.Background()
	resp, err := c.cli.ContainerExecCreate(ctx, c.Config.ID, types.ExecConfig{
		Tty:          true,
		AttachStdout: true,
		Cmd:          []string{"sh", "-c", cmd},
	})
	if err != nil {
		return nil, err
	}
	attach, err := c.cli.ContainerExecAttach(ctx, resp.ID, types.ExecStartCheck{
		Tty: true,
	})
	if err != nil {
		return nil, err
	}
	defer attach.Close()
	var lines []string
	for {
		bytes, _, err := attach.Reader.ReadLine()
		if err != nil {
			break
		}
		lines = append(lines, string(bytes))
	}
	return lines, nil
}

// GetAllEndpoints returns the public host, and map of private ports to list of public ports. May pass in optional
// private ports as args to filter out the returned results. None implies return all.
func (c *Container) GetAllEndpoints(privatePorts ...string) (string, map[string][]string, error) {
	network := c.Config.NetworkSettings.Networks[c.ServiceConfig.Network]
	if network == nil {
		return "", nil, fmt.Errorf("network not found for container %s", c.Config.Names[0])
	}
	if len(c.Config.Ports) == 0 {
		return "", nil, fmt.Errorf("no ports found for container %s", c.Config.Names[0])
	}
	portMap, err := parsePorts(c.Config.Ports, privatePorts...)
	if err != nil {
		return "", nil, fmt.Errorf("error parsing ports for container %s", c.Config.Names[0])
	}
	host := "127.0.0.1"
	if runtime.GOOS == "linux" && !isWSL() {
		host = network.Gateway
	}
	logrus.Printf("container: %s is running on host: %s, port-bindings: %v", c.Config.Names[0], host, c.Config.Ports)
	return host, portMap, nil
}

// GetEndpoint specialized version of GetAllEndpoints where we expect a single unique public port on the container. error
// is returned if that's not the case.
func (c *Container) GetEndpoint() (string, string, error) {
	host, ports, err := c.GetAllEndpoints()
	if err != nil {
		return "", "", err
	}
	var port string
	count := 0
	for _, publicPort := range ports {
		if count > 1 {
			return "", "", errors.New("multiple port bindings found")
		}
		if len(publicPort) > 1 {
			return "", "", fmt.Errorf("mulitple public ports found")
		}
		port = publicPort[0]
		count++
	}
	logrus.Printf("container: %s is running on host: %s, port: %s", c.Config.Names[0], host, port)
	return host, port, nil
}
