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

func (daemon *Daemon) NetworkCreate(name string, driver string, labels map[string]string) (string, error) {
	netdriver, err := daemon.networkCtrlr.NewNetworkDriver(driver, optionsOf(labels))
	if err != nil {
		return "", err
	}

	network, err := daemon.networkCtrlr.NewNetwork(netdriver, name, optionsOf(labels))
	if err != nil {
		return "", err
	}

	// Naughty, piggy back on other network lock
	daemon.libnetworks = append(daemon.libnetworks, network)
	return network.ID(), nil
}

func (daemon *Daemon) NetworkList() []types.NetworkResponse {
	var result []types.NetworkResponse
	for _, network := range daemon.libnetworks {
		result = append(result, types.NetworkResponse{
			ID:     network.ID(),
			Name:   network.Name(),
			Driver: network.Type(),
			//Labels: net.Labels(),
		})
	}
	return result
}

func (daemon *Daemon) NetworkGet(idOrName string) (int, libnetwork.Network, error) {
	for i, network := range daemon.libnetworks {
		if network.ID() == idOrName || network.Name() == idOrName {
			return i, network, nil
		}
	}

	return 0, nil, fmt.Errorf("Not found")
}

func (daemon *Daemon) NetworkDestroy(idOrName string) error {
	i, network, err := daemon.NetworkGet(idOrName)
	if err != nil {
		return err
	}

	if err := network.Delete(); err != nil {
		return err
	}

	daemon.libnetworks = daemon.libnetworks[:i+copy(daemon.libnetworks[i:], daemon.libnetworks[i+1:])]
	return nil
}

func (daemon *Daemon) endpointOnNetworkLib(namesOrId, containerID string, labels map[string]string) (libnetwork.Endpoint, error) {
	_, network, err := daemon.NetworkGet(namesOrId)
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
