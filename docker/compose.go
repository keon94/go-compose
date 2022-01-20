package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

type (
	// Compose an API to access docker-compose
	Compose struct {
		cli      *client.Client
		config   ComposeConfig
		services map[string]string
	}

	// EnvironmentConfig global-level (i.e. for all containers) config for the testing framework
	EnvironmentConfig struct {
		// UpTimeout timeout for docker-compose up
		UpTimeout time.Duration
		// DownTimeout timeout for docker-compose down
		DownTimeout time.Duration
		// ComposeFilePaths the path to the compose-YAML file(s)
		ComposeFilePaths []string
		// Optional custom container label name
		Label string
	}
	// ServiceConfig service/container-level config needed for docker-compose purposes
	ServiceConfig struct {
		// Name Service name (must correspond to the name found in the compose file)
		Name string
		// EnvironmentVars optional set of key-value pairs to pass to the service (note, these must be globally unique)
		EnvironmentVars map[string]string
		// Optional custom network name
		Network string
	}
	// ComposeConfig config needed to get docker-compose and the testing framework going
	ComposeConfig struct {
		// Env the global config for this composes' execution
		Env *EnvironmentConfig
		// Services maps service names to their config, for those managed by this composes' execution
		Services map[string]*ServiceConfig
	}
)

func NewCompose(params ComposeConfig) (*Compose, error) {
	if len(params.Env.ComposeFilePaths) == 0 {
		return nil, fmt.Errorf("at least one compose file must be specified")
	}
	if params.Env.Label == "" {
		params.Env.Label = DefaultLabel
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
	startTime := time.Now()
	if err := runCommand(cmd, c.config.Env.UpTimeout); err != nil {
		return err
	}
	timeout := c.config.Env.UpTimeout - time.Now().Sub(startTime)
	if err := c.awaitState(timeout, c.awaitStart); err != nil {
		return fmt.Errorf("error with compose-up: %w", err)
	}
	logger.Infof("Brought up services %v", c.getServiceNames())
	for _, service := range c.config.Services {
		c.services[service.Name] = ""
	}
	return nil
}

func (c *Compose) Down() error {
	pathsArgs := c.getComposeFileArgs()
	args := append(pathsArgs, []string{"-p", ProjectID, "down", "-v"}...)
	cmd := exec.Command("docker-compose", args...)
	startTime := time.Now()
	if err := runCommand(cmd, c.config.Env.DownTimeout); err != nil {
		return err
	}
	timeout := c.config.Env.UpTimeout - time.Now().Sub(startTime)
	if err := c.awaitState(timeout, c.awaitStop); err != nil {
		return fmt.Errorf("error with compose-down: %w", err)
	}
	logger.Infof("Brought down services %v", c.getServiceNames())
	return nil
}

func (c *Compose) GetContainer(service string) (*Container, error) {
	list, err := c.cli.ContainerList(context.Background(), types.ContainerListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", c.config.Env.Label),
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
	return &Container{
		cli:           c.cli,
		Config:        &list[0],
		ServiceConfig: c.config.Services[service],
	}, nil
}

func (c *Compose) awaitState(timeout time.Duration, serviceFn func(service *ServiceConfig, timeout <-chan time.Time) error) error {
	pool := new(sync.WaitGroup)
	waiter := make(chan interface{})
	errorMap := new(sync.Map)
	pool.Add(len(c.config.Services))
	timer := time.After(timeout)
	for _, service := range c.config.Services {
		service := service
		go func() {
			err := serviceFn(service, timer)
			if err != nil {
				errorMap.Store(service.Name, err)
				waiter <- nil
			}
			pool.Done()
		}()
	}
	go func() {
		pool.Wait()
		waiter <- nil
	}()
	select {
	case <-waiter:
		if !IsEmpty(errorMap) {
			return fmt.Errorf("error waiting for services. errors captured: \n%v\n", PrintMap(errorMap))
		}
		return nil
	}
}

func (c *Compose) awaitStart(service *ServiceConfig, timeout <-chan time.Time) error {
	for {
		container, e := c.GetContainer(service.Name)
		if e != nil {
			return fmt.Errorf("error getting container for %s: %w", service, e)
		}
		if container != nil {
			status := container.GetStatus()
			if err := status.Error; err != nil {
				return err
			}
			if !(status.Code == Unhealthy || status.Code == NotReady) {
				return nil
			}
		}
		select {
		case <-timeout:
			return fmt.Errorf("service %s startup timed out", service.Name)
		default:
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func (c *Compose) awaitStop(service *ServiceConfig, timeout <-chan time.Time) error {
	for {
		container, e := c.GetContainer(service.Name)
		if e != nil {
			return fmt.Errorf("error getting container for %s: %w", service, e)
		}
		if container == nil {
			return nil
		}
		status := container.GetStatus()
		if err := status.Error; err != nil {
			return err
		}
		if status.Code != Running {
			return nil
		}
		select {
		case <-timeout:
			return fmt.Errorf("service %s shutdown timed out", service.Name)
		default:
			time.Sleep(500 * time.Millisecond)
		}
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
