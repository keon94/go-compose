package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
)

type (
	// Compose an API to access docker-compose
	Compose struct {
		cli      *client.Client
		config   ComposeConfig
		services map[string]string
	}
	// Container wrapped API for docker containers
	Container struct {
		cli    *client.Client
		Config *types.Container
	}
	// EnvironmentConfig global-level (i.e. for all containers) config for the testing framework
	EnvironmentConfig struct {
		// UpTimeout timeout for docker-compose up
		UpTimeout time.Duration
		// DownTimeout timeout for docker-compose down
		DownTimeout time.Duration
		// ComposeFilePaths the path to the compose-YAML file(s)
		ComposeFilePaths []string
	}
	// ServiceConfig service/container-level config needed for docker-compose purposes
	ServiceConfig struct {
		// Name Service name (must correspond to the name found in the compose file)
		Name string
		// EnvironmentVars optional set of key-value pairs to pass to the service (note, these must be globally unique)
		EnvironmentVars map[string]string
	}
	// ComposeConfig config needed to get docker-compose and the testing framework going
	ComposeConfig struct {
		// Env the global config for this composes' execution
		Env *EnvironmentConfig
		// Services maps service names to their config, for those managed by this composes' execution
		Services map[string]*ServiceConfig
	}
	containerStatusChecker func(*Container) (bool, *ContainerStatus)
)

func NewCompose(params ComposeConfig) (*Compose, error) {
	if len(params.Env.ComposeFilePaths) == 0 {
		return nil, fmt.Errorf("at least one compose file must be specified")
	}
	for _, path := range params.Env.ComposeFilePaths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil, fmt.Errorf("compose file not found at %s", path)
		}
	}
	compose := Compose{
		config:   params,
		services: make(map[string]string),
	}
	var err error
	compose.cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &compose, nil
}

func (c *Compose) Up() error {
	pathsArgs := c.getComposeFileArgs()
	args := append(pathsArgs, []string{"-p", ProjectID, "up", "-d", "--renew-anon-volumes"}...)
	args = append(args, c.getServiceNames()...)
	cmd := exec.Command("docker-compose", args...)
	cmd.Env = c.getEnvVariables()
	if err := runCommand(cmd); err != nil {
		return err
	}
	if err := c.awaitState(c.config.Env.UpTimeout, func(c *Container) (bool, *ContainerStatus) {
		if c == nil {
			return false, nil
		}
		status := c.GetStatus()
		return status.Code == Running, status
	}); err != nil {
		return fmt.Errorf("error with compose-up: %w", err)
	}
	logrus.Infof("Brought up services %v", c.getServiceNames())
	for _, service := range c.config.Services {
		c.services[service.Name] = ""
	}
	return nil
}

func (c *Compose) Down() error {
	pathsArgs := c.getComposeFileArgs()
	args := append(pathsArgs, []string{"-p", ProjectID, "down", "-v"}...)
	cmd := exec.Command("docker-compose", args...)
	if err := runCommand(cmd); err != nil {
		return err
	}
	if err := c.awaitState(c.config.Env.DownTimeout, func(c *Container) (bool, *ContainerStatus) {
		if c == nil {
			return true, nil
		}
		status := c.GetStatus()
		return status.Code != Running, nil
	}); err != nil {
		return fmt.Errorf("error with compose-down: %w", err)
	}
	logrus.Infof("Brought down services %v", c.getServiceNames())
	return nil
}

func (c *Compose) GetContainer(service string) (*Container, error) {
	list, err := c.cli.ContainerList(context.Background(), types.ContainerListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", Label),
			filters.Arg("name", service),
		),
	})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	} else if len(list) > 1 {
		return nil, errors.New("Returned incorrect count of containers for service " + service)
	}
	return &Container{cli: c.cli, Config: &list[0]}, nil
}

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

func (c *Compose) awaitState(timeout time.Duration, statusChecker containerStatusChecker) error {
	pool := new(sync.WaitGroup)
	waiter := make(chan struct{})
	errorMap := new(sync.Map)
	pool.Add(len(c.config.Services))
	for _, service := range c.config.Services {
		service := service
		go func() {
			c.awaitServiceState(service, statusChecker, errorMap)
			pool.Done()
		}()
	}
	go func() {
		defer close(waiter)
		pool.Wait()
	}()
	timer := time.After(timeout)
	select {
	case <-waiter:
		if !IsEmpty(errorMap) {
			return fmt.Errorf("error waiting for services. errors captured: %v", PrintMap(errorMap))
		}
		return nil
	case <-timer:
		return fmt.Errorf("timed out waiting for services. errors captured: %v", PrintMap(errorMap))
	}
}

func (c *Compose) awaitServiceState(service *ServiceConfig, statusChecker containerStatusChecker, errorMap *sync.Map) {
	success := false
	for {
		container, e := c.GetContainer(service.Name)
		if e != nil {
			errorMap.Store(service, fmt.Errorf("error getting container for %s: %w", service, e))
			break
		}
		if ok, status := statusChecker(container); true {
			if ok {
				success = true
				break
			} else if status != nil {
				if status.Error != nil {
					errorMap.Store(service, status)
				}
				if status.Code == Unhealthy || status.Code == NotReady {
					continue
				} else {
					break
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if success {
		errorMap.Delete(service) // in case we had anything
	}
}

func (c *Compose) getEnvVariables() []string {
	var envs []string
	for _, cfg := range c.config.Services {
		for k, v := range cfg.EnvironmentVars {
			envs = append(envs, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return envs
}

func runCommand(cmd *exec.Cmd) error {
	return RunProcessWithLogs(cmd, func(msg string) {
		ColoredPrintf(msg + "\n")
		//fmt.Printf("%s\n", msg)
	})
}

func (c *Compose) getServiceNames() []string {
	var names []string
	for name := range c.config.Services {
		names = append(names, name)
	}
	return names
}

func (c *Compose) getComposeFileArgs() []string {
	var cmd []string
	for _, path := range c.config.Env.ComposeFilePaths {
		cmd = append(cmd, "-f")
		cmd = append(cmd, path)
	}
	return cmd
}
