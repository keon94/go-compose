package docker

import (
	"fmt"
)

type (
	Environment struct {
		// Services maps service names to their data (output of their handlers). See ServiceHandler
		Services      map[string]interface{}
		shutdownHooks []func()
		afterHandlers []AfterHandler
		compose       *Compose
	}
	ServiceEntry struct {
		//Name see ServiceConfig.Name
		Name string
		// DisableShutdownLogs set to true to disable printing logs for this container on shutdown (optional)
		DisableShutdownLogs bool
		// Handler Function to extract relevant data from the service's container for the test's needs (optional, but
		// usually needed by tests/consumers)
		Handler ServiceHandler
		// Before Function to run before container startup (optional)
		Before BeforeHandler
		// Before Function to run after container shutdown (optional)
		After AfterHandler
		// EnvironmentVars env variables for the service
		EnvironmentVars map[string]string
		// Network optional network name, otherwise defaults to the Network const
		Network string
	}
	BeforeHandler  func() error
	ServiceHandler func(*Container) (interface{}, error)
	AfterHandler   func()
)

func StartEnvironment(config *EnvironmentConfig, entries ...*ServiceEntry) *Environment {
	services := mapServiceEntries(entries...)
	beforeHandlers, afterHandlers := getHandlers(services)
	serviceConfigs := getServiceConfigs(services)
	compose, err := NewCompose(ComposeConfig{
		Env:      config,
		Services: serviceConfigs,
	})
	if err != nil {
		logger.Fatal(err)
	}
	env := &Environment{
		compose:       compose,
		afterHandlers: afterHandlers,
	}
	env.start(beforeHandlers, services)
	return env
}

func (e *Environment) start(beforeHandlers []BeforeHandler, entries map[string]*ServiceEntry) {
	_ = e.compose.Down() //do this in case of a running state...
	for _, before := range beforeHandlers {
		if err := before(); err != nil {
			logger.Fatal(err)
		}
	}
	e.addShutdownHooks(entries, func(config *ServiceEntry, container *Container) {
		if !config.DisableShutdownLogs {
			PrintLogs(container)
		}
	})
	err := e.compose.Up()
	if err != nil {
		e.Shutdown()
		logger.Fatal(err)
	}
	err = e.invokeServiceHandlers(entries)
	if err != nil {
		e.Shutdown()
		logger.Fatal(err)
	}
}

// Shutdown MUST be used by tests' cleanup functions or there may be container leaks
func (e *Environment) Shutdown() {
	for _, hook := range e.shutdownHooks {
		hook()
	}
	err := e.compose.Down()
	if err != nil {
		logger.Error(err)
	}
	for _, after := range e.afterHandlers {
		after()
	}
}

func (e *Environment) addShutdownHooks(entries map[string]*ServiceEntry, hook func(config *ServiceEntry, container *Container)) {
	for _, config := range entries {
		config := config
		e.shutdownHooks = append(e.shutdownHooks, func() {
			container, err := e.compose.GetContainer(config.Name)
			if container == nil {
				return
			}
			if err != nil {
				logger.Errorf("can't run container shutdown hook. err getting container for service %s", config.Name)
			}
			if container == nil {
				logger.Errorf("can't run container shutdown hook. no container found for service %s", config.Name)
			}
			hook(config, container)
		})
	}
}

func (e *Environment) invokeServiceHandlers(entries map[string]*ServiceEntry) error {
	serviceOutputs := make(map[string]interface{})
	for serviceName, config := range entries {
		container, err := e.compose.GetContainer(serviceName)
		if err != nil {
			return err
		}
		if container == nil {
			return fmt.Errorf("no container found for service %s", serviceName)
		}
		var output interface{}
		if config.Handler != nil {
			logger.Infof("running handler for service %s", serviceName)
			output, err = config.Handler(container)
			if err != nil {
				return err
			}
		} else {
			logger.Infof("no handler found for service %s", serviceName)
		}
		serviceOutputs[serviceName] = output
	}
	e.Services = serviceOutputs
	return nil
}

func mapServiceEntries(entries ...*ServiceEntry) map[string]*ServiceEntry {
	services := make(map[string]*ServiceEntry)
	for _, e := range entries {
		services[e.Name] = e
	}
	return services
}

func getServiceConfigs(entries map[string]*ServiceEntry) map[string]*ServiceConfig {
	serviceConfigs := make(map[string]*ServiceConfig)
	for serviceName, entry := range entries {
		cfg := &ServiceConfig{
			Name:            entry.Name,
			EnvironmentVars: entry.EnvironmentVars,
			Network:         entry.Network,
		}
		if cfg.Network == "" {
			cfg.Network = DefaultNetwork
		}
		serviceConfigs[serviceName] = cfg
	}
	return serviceConfigs
}

func getHandlers(entries map[string]*ServiceEntry) ([]BeforeHandler, []AfterHandler) {
	var beforeHandlers []BeforeHandler
	var afterHandlers []AfterHandler
	for _, entry := range entries {
		if entry.Before != nil {
			beforeHandlers = append(beforeHandlers, entry.Before)
		}
		if entry.After != nil {
			afterHandlers = append(afterHandlers, entry.After)
		}
	}
	return beforeHandlers, afterHandlers
}
