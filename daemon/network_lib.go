package daemon

import (
	"fmt"

	_ "github.com/Sirupsen/logrus"

	"github.com/docker/libnetwork"
	"github.com/docker/libnetwork/pkg/options"

	"github.com/docker/docker/api/types"
)

func optionsOf(labels map[string]string) options.Generic {
	var options options.Generic
	for k, v := range labels {
		options[k] = v
	}
	return options
}

func (daemon *Daemon) NetworkConfigure(driver string, labels map[string]string) error {
	return daemon.networkCtrlr.ConfigureNetworkDriver(driver, optionsOf(labels))
}

func (daemon *Daemon) NetworkCreate(name string, driver string, labels map[string]string) (string, error) {
	network, err := daemon.networkCtrlr.NewNetwork(driver, name, optionsOf(labels))
	if err != nil {
		return "", err
	}
	return network.ID(), nil
}

func (daemon *Daemon) NetworkList() []types.NetworkResponse {
	var result []types.NetworkResponse
	daemon.networkCtrlr.WalkNetworks(func(network libnetwork.Network) bool {
		result = append(result, types.NetworkResponse{
			ID:     network.ID(),
			Name:   network.Name(),
			Driver: network.Type(),
			//Labels: net.Labels(),
		})
		return false
	})
	return result
}

func (daemon *Daemon) NetworkGet(idOrName string) (libnetwork.Network, error) {
	var network libnetwork.Network
	found := daemon.networkCtrlr.WalkNetworks(func(candidate libnetwork.Network) bool {
		if candidate.ID() == idOrName || candidate.Name() == idOrName {
			network = candidate
			return true
		}
		return false
	})

	if found {
		return network, nil
	}
	return nil, fmt.Errorf("Not found")
}

func (daemon *Daemon) NetworkDestroy(idOrName string) error {
	network, err := daemon.NetworkGet(idOrName)
	if err != nil {
		return err
	}

	return network.Delete()
}

func (daemon *Daemon) endpointOnNetworkLib(namesOrId, containerID string, labels map[string]string) (libnetwork.Endpoint, error) {
	network, err := daemon.NetworkGet(namesOrId)
	if err != nil {
		return nil, err
	}

	endpoint, err := network.CreateEndpoint("", containerID, optionsOf(labels))
	return endpoint, err
}

func (daemon *Daemon) endpointsOnNetworksLib(namesOrIds []string, containerID string) ([]libnetwork.Endpoint, error) {
	var result []libnetwork.Endpoint
	for _, nameOrId := range namesOrIds {
		endpoint, err := daemon.endpointOnNetworkLib(nameOrId, containerID, nil)
		if err != nil {
			return nil, err
		}
		result = append(result, endpoint)
	}
	return result, nil
}

func (daemon *Daemon) NetworkPlug(containerID, nameOrId string, labels map[string]string) (string, error) {
	container, err := daemon.Get(containerID)
	if err != nil {
		return "", fmt.Errorf("Container '%s' not found", containerID)
	}

	if container.State.IsRunning() {
		return "", fmt.Errorf("Cannot plug in running container (yet)")
	}

	endpoint, err := daemon.endpointOnNetworkLib(nameOrId, container.ID, labels)
	if err != nil {
		return "", err
	}

	container.LibNetworkEndpoints = append(container.LibNetworkEndpoints, endpoint)
	return endpoint.ID(), nil
}

func (c *Container) GetEndpointLib(nameOrID string) (int, libnetwork.Endpoint, error) {
	for i, endpoint := range c.LibNetworkEndpoints {
		if endpoint.ID() == nameOrID {
			return i, endpoint, nil
		}
	}
	return 0, nil, fmt.Errorf("Not found")
}

func (daemon *Daemon) NetworkUnplug(containedID, endpointID string) error {
	container, err := daemon.Get(containedID)
	if err != nil {
		return err
	}

	if container.State.IsRunning() {
		return fmt.Errorf("Cannot unplug running container (yet)")
	}

	i, endpoint, err := container.GetEndpointLib(endpointID)
	if err != nil {
		return err
	}

	if err := endpoint.Delete(); err != nil {
		return err
	}

	container.LibNetworkEndpoints = container.LibNetworkEndpoints[:i+copy(
		container.LibNetworkEndpoints[i:], container.LibNetworkEndpoints[i+1:])]
	return nil
}
