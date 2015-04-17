package daemon

import (
	"fmt"

	_ "github.com/Sirupsen/logrus"
	"github.com/docker/docker/api/types"
	"github.com/docker/libnetwork/pkg/options"
)

func (daemon *Daemon) LibNetworkCreate(name string, driver string, labels map[string]string) (string, error) {
	var options options.Generic
	for k, v := range labels {
		options[k] = v
	}

	netdriver, err := daemon.networkCtrlr.NewNetworkDriver(driver, options)
	if err != nil {
		return "", err
	}

	network, err := daemon.networkCtrlr.NewNetwork(netdriver, name, options)
	if err != nil {
		return "", err
	}

	// Naughty, piggy back on other network lock
	daemon.networks.Lock()
	defer daemon.networks.Unlock()
	daemon.libnetworks = append(daemon.libnetworks, network)
	return network.ID(), nil
}

func (daemon *Daemon) LibNetworkList() []types.NetworkResponse {
	daemon.networks.Lock()
	defer daemon.networks.Unlock()

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

func (daemon *Daemon) LibNetworkDestroy(idOrName string) error {
	daemon.networks.Lock()
	defer daemon.networks.Unlock()

	for i, network := range daemon.libnetworks {
		if network.ID() == idOrName || network.Name() == idOrName {
			if err := network.Delete(); err != nil {
				return err
			}

			daemon.libnetworks = append(daemon.libnetworks[:i], daemon.libnetworks[i+1:]...)
			break
		}
	}

	return nil
}

//func (daemon *Daemon) endpointOnNetwork(namesOrId string, labels map[string]string) (*Endpoint, error) {
//	net := daemon.networks.Get(namesOrId)
//	if net == nil {
//		return nil, fmt.Errorf("Network '%s' not found", namesOrId)
//	}
//
//	return &Endpoint{
//		ID:      stringid.GenerateRandomID(),
//		Network: net.ID,
//		Labels:  labels,
//	}, nil
//}
//
//func (daemon *Daemon) endpointsOnNetworks(namesOrIds []string) ([]*Endpoint, error) {
//	var result []*Endpoint
//	for _, nameOrId := range namesOrIds {
//		endpoint, err := daemon.endpointOnNetwork(nameOrId, nil)
//		if err != nil {
//			return nil, err
//		}
//		result = append(result, endpoint)
//	}
//	return result, nil
//}

func (daemon *Daemon) LibNetworkPlug(containerID, nameOrId string, labels map[string]string) (string, error) {
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

func (daemon *Daemon) LibNetworkUnplug(containedID, endpointID string) error {
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
