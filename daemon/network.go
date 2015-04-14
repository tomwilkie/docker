package daemon

import (
	"fmt"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon/execdriver"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/daemon/networkdriver/simplebridge"
	"github.com/docker/docker/pkg/namesgenerator"
	"github.com/docker/docker/pkg/stringid"
)

type NetworkRegistry struct {
	sync.Mutex
	networks map[string]*Network
}

type Network struct {
	ID     string
	Name   string
	Driver string
	Labels map[string]string
}

type Endpoint struct {
	ID      string
	Network string
	Labels  map[string]string
}

type Driver interface {
	Create(network *Network) error
	Destroy(network *Network) error

	Plug(network *Network, endpoint *Endpoint) (*execdriver.NetworkInterface, error)
	Unplug(network *Network, endpoint *Endpoint) error
}

func (daemon *Daemon) NetworkCreate(name string, driver string, labels map[string]string) (string, error) {
	daemon.networks.Lock()
	defer daemon.networks.Unlock()

	if name == "" {
		for i := 0; true; i++ {
			name = namesgenerator.GetRandomName(i)
			if !daemon.networks.ExistsWithName(name) {
				break
			}
		}
	}

	if daemon.networks.ExistsWithName(name) {
		return "", fmt.Errorf("Network '%s' already exists", name)
	}

	net, err := NewNetwork(name, driver, labels)
	if err != nil {
		return "", err
	}

	daemon.networks.Add(net)
	return net.ID, nil
}

func (daemon *Daemon) NetworkList() []types.NetworkResponse {
	daemon.networks.Lock()
	defer daemon.networks.Unlock()

	var result []types.NetworkResponse
	daemon.networks.Walk(func(net *Network) {
		result = append(result, types.NetworkResponse{
			ID:     net.ID,
			Name:   net.Name,
			Driver: net.Driver,
			Labels: net.Labels,
		})
	})
	return result
}

func (daemon *Daemon) NetworkDestroy(id string) error {
	daemon.networks.Lock()
	defer daemon.networks.Unlock()

	net := daemon.networks.Get(id)
	if net == nil {
		return fmt.Errorf("Network '%s' not found", id)
	}

	if err := net.Destroy(); err != nil {
		return err
	}

	daemon.networks.Remove(id)
	return nil
}

func (daemon *Daemon) endpointOnNetwork(namesOrId string, labels map[string]string) (*Endpoint, error) {
	net := daemon.networks.Get(namesOrId)
	if net == nil {
		return nil, fmt.Errorf("Network '%s' not found", namesOrId)
	}

	return &Endpoint{
		ID:      stringid.GenerateRandomID(),
		Network: net.ID,
		Labels:  labels,
	}, nil
}

func (daemon *Daemon) endpointsOnNetworks(namesOrIds []string) ([]*Endpoint, error) {
	var result []*Endpoint
	for _, nameOrId := range namesOrIds {
		endpoint, err := daemon.endpointOnNetwork(nameOrId, nil)
		if err != nil {
			return nil, err
		}
		result = append(result, endpoint)
	}
	return result, nil
}

func (daemon *Daemon) NetworkPlug(containerID, nameOrId string, labels map[string]string) (string, error) {
	daemon.networks.Lock()
	defer daemon.networks.Unlock()

	container, err := daemon.Get(containerID)
	if err != nil {
		return "", fmt.Errorf("Container '%s' not found", containerID)
	}

	if container.State.IsRunning() {
		return "", fmt.Errorf("Cannot plug in running container (yet)")
	}

	endpoint, err := daemon.endpointOnNetwork(nameOrId, labels)
	if err != nil {
		return "", err
	}

	container.Endpoints = append(container.Endpoints, endpoint)
	return endpoint.ID, nil
}

func (daemon *Daemon) NetworkUnplug(containedID, endpointID string) error {
	daemon.networks.Lock()
	defer daemon.networks.Unlock()

	container, err := daemon.Get(containedID)
	if err != nil {
		return err
	}

	if container.State.IsRunning() {
		return fmt.Errorf("Cannot unplug running container (yet)")
	}

	i, endpoint := container.GetEndpoint(endpointID)
	if endpoint == nil {
		return fmt.Errorf("Endpoint '%s' not found", endpointID)
	}

	container.Endpoints = container.Endpoints[:i+copy(container.Endpoints[i:], container.Endpoints[i+1:])]

	return nil
}

func NewNetworkRegistry() NetworkRegistry {
	return NetworkRegistry{networks: make(map[string]*Network)}
}

func (reg *NetworkRegistry) ExistsWithName(name string) bool {
	for _, net := range reg.networks {
		if net.Name == name {
			return true
		}
	}
	return false
}

func (reg *NetworkRegistry) Add(network *Network) {
	reg.networks[network.ID] = network
}

func (reg *NetworkRegistry) Get(nameOrId string) *Network {
	network, okay := reg.networks[nameOrId]
	if okay {
		return network
	}

	for _, network := range reg.networks {
		if network.Name == nameOrId {
			return network
		}
	}

	return nil
}

func (reg *NetworkRegistry) Remove(id string) {
	delete(reg.networks, id)
}

func (reg *NetworkRegistry) Walk(f func(network *Network)) {
	for _, network := range reg.networks {
		f(network)
	}
}

func (reg *NetworkRegistry) Shutdown() {
	reg.Lock()
	defer reg.Unlock()

	for _, network := range reg.networks {
		network.Destroy()
	}
}

func NewNetwork(networkName, driverName string, labels map[string]string) (*Network, error) {
	driver, okay := GetNetworkDriver(driverName)
	if !okay {
		return nil, fmt.Errorf("Driver '%s' not found", driverName)
	}

	network := &Network{
		Driver: driverName,
		ID:     stringid.GenerateRandomID(),
		Name:   networkName,
		Labels: labels,
	}
	if err := driver.Create(network); err != nil {
		return nil, err
	}

	return network, nil
}

func (net *Network) Destroy() error {
	driver, okay := GetNetworkDriver(net.Driver)
	if !okay {
		return fmt.Errorf("Driver '%s' not found", net.Driver)
	}

	if err := driver.Destroy(net); err != nil {
		return err
	}

	return nil
}

func (e *Endpoint) Plug(daemon *Daemon) (*execdriver.NetworkInterface, error) {
	net := daemon.networks.Get(e.Network)
	if net == nil {
		return nil, fmt.Errorf("Network '%s' not found", e.Network)
	}

	driver, okay := GetNetworkDriver(net.Driver)
	if !okay {
		return nil, fmt.Errorf("Driver not found")
	}

	inf, err := driver.Plug(net, e)
	if err != nil {
		return nil, err
	}

	logrus.Infof("Plug %+v", inf)

	return inf, nil
}

func (e *Endpoint) Unplug(daemon *Daemon) error {
	net := daemon.networks.Get(e.Network)
	if net == nil {
		return fmt.Errorf("Network '%s' not found", e.Network)
	}

	driver, okay := GetNetworkDriver(net.Driver)
	if !okay {
		return fmt.Errorf("Driver not found")
	}

	if err := driver.Unplug(net, e); err != nil {
		return err
	}

	return nil
}

var drivers map[string]Driver

func init() {
	drivers = make(map[string]Driver)
	RegisterNetworkDriver("simplebridge", simpleBridgeDriver{make(map[string]*simplebridge.Network)})
}

func GetNetworkDriver(name string) (Driver, bool) {
	driver, okay := drivers[name]
	return driver, okay
}

func RegisterNetworkDriver(name string, driver Driver) {
	drivers[name] = driver
}

// SimpleBridge drivers
type simpleBridgeDriver struct {
	networks map[string]*simplebridge.Network
}

func (s simpleBridgeDriver) Create(network *Network) error {
	net, err := simplebridge.NewNetwork(network.Labels)
	if err != nil {
		return err
	}

	err = net.Setup()
	if err != nil {
		return err
	}

	s.networks[network.ID] = net
	return nil
}

func (s simpleBridgeDriver) Destroy(network *Network) error {
	net, found := s.networks[network.ID]
	if !found {
		return fmt.Errorf("Network '%s' not found", network.ID)
	}

	delete(s.networks, network.ID)
	return net.Destroy()
}

func (s simpleBridgeDriver) Plug(network *Network, endpoint *Endpoint) (*execdriver.NetworkInterface, error) {
	net, found := s.networks[network.ID]
	if !found {
		return nil, fmt.Errorf("Network '%s' not found", network.ID)
	}

	inf, err := net.Allocate(endpoint.ID, nil)
	if err != nil {
		return nil, err
	}

	return inf, nil
}

func (s simpleBridgeDriver) Unplug(network *Network, endpoint *Endpoint) error {
	net, found := s.networks[network.ID]
	if !found {
		return fmt.Errorf("Network '%s' not found", network.ID)
	}

	err := net.Release(endpoint.ID)
	return err
}
