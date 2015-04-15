package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/daemon/execdriver"
	"github.com/docker/docker/daemon/networkdriver/simplebridge"
	"github.com/docker/docker/pkg/namesgenerator"
	"github.com/docker/docker/pkg/stringid"
)

type NetworkRegistry struct {
	sync.Mutex
	path     string
	networks map[string]*Network
}

type Network struct {
	ID     string
	Name   string
	Driver string
	Labels map[string]string // Labels are treated as user-defined input
	State  map[string]string // State is owned by the driver, for its use
}

type Endpoint struct {
	ID      string
	Network string
	Labels  map[string]string
}

type Driver interface {
	Setup(network *Network) error
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

	net := &Network{
		Driver: driver,
		ID:     stringid.GenerateRandomID(),
		Name:   name,
		Labels: labels,
		State:  make(map[string]string),
	}

	if err := net.Setup(); err != nil {
		return "", err
	}

	if err := daemon.networks.Add(net); err != nil {
		return "", err
	}
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

	return daemon.networks.Remove(net.ID)
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

func NewNetworkRegistry(path string) NetworkRegistry {
	return NetworkRegistry{
		path:     path,
		networks: make(map[string]*Network),
	}
}

func (reg *NetworkRegistry) Restore() error {
	logrus.Infof("Loading networks from '%s'", reg.path)
	dir, err := ioutil.ReadDir(reg.path)
	if err != nil {
		return err
	}

	for _, v := range dir {
		id := v.Name()
		file, err := os.Open(path.Join(reg.path, id))
		if err != nil {
			return err
		}

		network := &Network{}
		if err := json.NewDecoder(file).Decode(network); err != nil {
			logrus.Errorf("Failed to load network %v: %v", id, err)
			return err
		}
		if network.State == nil {
			network.State = make(map[string]string)
		}

		if err := network.Setup(); err != nil {
			logrus.Errorf("Failed to setup network %v: %v", id, err)
			return err
		}

		logrus.Infof("Loaded network %s", network.ID)
		reg.Add(network)
	}

	return nil
}

func (reg *NetworkRegistry) ExistsWithName(name string) bool {
	for _, net := range reg.networks {
		if net.Name == name {
			return true
		}
	}
	return false
}

func (reg *NetworkRegistry) Add(network *Network) error {
	if err := reg.save(network); err != nil {
		return err
	}
	reg.networks[network.ID] = network
	return nil
}

func (reg *NetworkRegistry) save(network *Network) error {
	var buf bytes.Buffer
	err := json.NewEncoder(&buf).Encode(network)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(path.Join(reg.path, network.ID), buf.Bytes(), 0666)
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

func (reg *NetworkRegistry) Remove(id string) error {
	if err := os.Remove(path.Join(reg.path, id)); err != nil {
		return err
	}
	delete(reg.networks, id)
	return nil
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

func (net *Network) Setup() error {
	driver, okay := GetNetworkDriver(net.Driver)
	if !okay {
		return fmt.Errorf("Driver '%s' not found", net.Driver)
	}

	return driver.Setup(net)
}

func (net *Network) Destroy() error {
	driver, okay := GetNetworkDriver(net.Driver)
	if !okay {
		return fmt.Errorf("Driver '%s' not found", net.Driver)
	}

	return driver.Destroy(net)
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
	RegisterNetworkDriver("simplebridge", &simpleBridgeDriver{make(map[string]*simplebridge.Network)})
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

func (s *simpleBridgeDriver) Setup(network *Network) error {
	state, found := network.State["simplebridge"]
	var net *simplebridge.Network
	var err error
	if found {
		net, err = simplebridge.FromJson(state)
		if err != nil {
			return err
		}
	} else {
		net, err = simplebridge.NewNetwork(network.Labels)
		if err != nil {
			return err
		}
	}

	if err = net.Setup(); err != nil {
		return err
	}

	state, err = net.ToJson()
	if err != nil {
		return err
	}
	network.State["simplebridge"] = state
	s.networks[network.ID] = net
	return nil
}

func (s *simpleBridgeDriver) Destroy(network *Network) error {
	net, found := s.networks[network.ID]
	if !found {
		return fmt.Errorf("Network '%s' not found", network.ID)
	}

	delete(s.networks, network.ID)
	return net.Destroy()
}

func (s *simpleBridgeDriver) Plug(network *Network, endpoint *Endpoint) (*execdriver.NetworkInterface, error) {
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

func (s *simpleBridgeDriver) Unplug(network *Network, endpoint *Endpoint) error {
	net, found := s.networks[network.ID]
	if !found {
		return fmt.Errorf("Network '%s' not found", network.ID)
	}

	err := net.Release(endpoint.ID)
	return err
}
