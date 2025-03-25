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
		noShutdown    bool
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
	serviceConfigs := getServiceConfigsMap(mapServiceEntries(entries...))
	compose, err := NewCompose(ComposeConfig{
		Env:      config,
		Services: serviceConfigs,
	})
	if err != nil {
		logger.Fatal(err)
	}
	env := &Environment{
		compose:    compose,
		noShutdown: config.NoShutdown,
	}
	if !config.NoCleanup {
		_ = env.compose.Down() //do this in case of a running state...
	}
	env.setupServiceConfigs(entries...)
	err = env.compose.Up()
	if err != nil {
		if !config.NoShutdown {
			env.Shutdown()
		}
		logger.Fatal(err)
	}
	err = env.invokeServiceHandlers(entries...)
	if err != nil {
		if !config.NoShutdown {
			env.Shutdown()
		}
		logger.Fatal(err)
	}
	return env
}

func (e *Environment) StartServices(entries ...*ServiceEntry) error {
	e.setupServiceConfigs(entries...)
	configs := getServiceConfigs(entries...)
	err := e.compose.Start(configs...)
	if err != nil {
		if stopErr := e.StopServices(getServiceNames(configs)...); stopErr != nil {
			logger.Warnf("could not call stop successfuly: %v", stopErr)
		}
		return err
	}
	err = e.invokeServiceHandlers(entries...)
	if err != nil {
		if stopErr := e.StopServices(getServiceNames(configs)...); stopErr != nil {
			logger.Warnf("could not call stop successfuly: %v", stopErr)
		}
		return err
	}
	return nil
}

func (e *Environment) StopServices(services ...string) error {
	configs := e.compose.getServiceConfigs(services...)
	if len(configs) != len(services) {
		return fmt.Errorf("can't stop unmanaged service contained in: %v", services)
	}
	err := e.compose.Stop(getServiceNames(configs)...)
	if err == nil {
		for _, service := range services {
			delete(e.Services, service)
		}
	}
	return err
}

// Shutdown MUST be used by tests' cleanup functions or there may be container leaks
func (e *Environment) Shutdown() {
	if e.noShutdown {
		return
	}
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
	// reset
	e.Services = make(map[string]interface{})
}

func (e *Environment) setupServiceConfigs(entries ...*ServiceEntry) {
	if len(entries) == 0 {
		return
	}
	services := mapServiceEntries(entries...)
	beforeHandlers, afterHandlers := getHandlers(services)
	e.afterHandlers = append(e.afterHandlers, afterHandlers...)
	for _, before := range beforeHandlers {
		if err := before(); err != nil {
			logger.Fatal(err)
		}
	}
	e.addShutdownHooks(services, func(config *ServiceEntry, container *Container) {
		if !config.DisableShutdownLogs {
			PrintLogs(GREEN, container)
		}
	})
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

func (e *Environment) invokeServiceHandlers(entries ...*ServiceEntry) error {
	serviceOutputs := make(map[string]interface{})
	for _, config := range entries {
		container, err := e.compose.GetContainer(config.Name)
		if err != nil {
			return err
		}
		if container == nil {
			return fmt.Errorf("no container found for service %s", config.Name)
		}
		var output interface{}
		if config.Handler != nil {
			logger.Infof("running handler for service %s", config.Name)
			output, err = config.Handler(container)
			if err != nil {
				return err
			}
		} else {
			logger.Infof("no handler found for service %s", config.Name)
		}
		serviceOutputs[config.Name] = output
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

func getServiceConfigsMap(entries map[string]*ServiceEntry) map[string]*ServiceConfig {
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

func getServiceConfigs(entries ...*ServiceEntry) []*ServiceConfig {
	var serviceConfigs []*ServiceConfig
	for _, entry := range entries {
		cfg := &ServiceConfig{
			Name:            entry.Name,
			EnvironmentVars: entry.EnvironmentVars,
			Network:         entry.Network,
		}
		if cfg.Network == "" {
			cfg.Network = DefaultNetwork
		}
		serviceConfigs = append(serviceConfigs, cfg)
	}
	return serviceConfigs
}

func getServiceNames(configs []*ServiceConfig) []string {
	var names []string
	for _, config := range configs {
		names = append(names, config.Name)
	}
	return names
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
