package plugins

import (
	"errors"
	"fmt"
)

// Temporary singleton
var Repo = NewRepository()

var ErrNotRegistered = errors.New("plugin type is not registered")

type RegisterFunc func(string, *Plugin) error

type Repository struct {
	supported map[string]RegisterFunc
	plugins   map[string]Plugins
}

type Plugins []*Plugin

func (repository *Repository) GetPlugins(kind string) (Plugins, error) {
	plugins, exists := repository.plugins[kind]
	// TODO: check whether 'kind' is a supportedPluginType
	if !exists {
		// If no plugins have been registered for this kind yet, that's
		// OK. Just set and return an empty list.
		repository.plugins[kind] = make([]*Plugin, 0)
		return repository.plugins[kind], nil
	}
	return plugins, nil
}

func NewRepository() *Repository {
	return &Repository{
		plugins:   make(map[string]Plugins),
		supported: make(map[string]RegisterFunc),
	}
}

func (repository *Repository) AddType(kind string, register RegisterFunc) error {
	if _, exists := repository.supported[kind]; exists {
		return fmt.Errorf("Registration for plugin kind '%s' already added", kind)
	}
	repository.supported[kind] = register
	return nil
}

func (repository *Repository) RegisterPlugin(addr string) error {
	plugin := &Plugin{addr: addr}
	resp, err := plugin.handshake()
	if err != nil {
		return fmt.Errorf("error in plugin handshake: %v", err)
	}

	for _, interest := range resp.InterestedIn {
		var (
			register RegisterFunc
			exists   bool
		)
		if register, exists = repository.supported[interest]; !exists {
			return fmt.Errorf("plugin type %s is not supported", interest)
		}

		if _, exists := repository.plugins[interest]; !exists {
			repository.plugins[interest] = []*Plugin{}
		}
		plugin.kind = interest
		repository.plugins[interest] = append(repository.plugins[interest], plugin)
		register(resp.Name, plugin)
	}

	return nil
}
