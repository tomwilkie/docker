package daemon

import (
	"encoding/json"
	"fmt"
	"github.com/docker/docker/daemon/execdriver"
	"github.com/docker/docker/plugins"
)

type netDriver struct {
	plugin *plugins.Plugin
}

func (driver *netDriver) Create(network *Network) error {
	reader, err := driver.plugin.Call("PUT", "/net/", network)
	defer reader.Close()
	return err
}

func (driver *netDriver) Destroy(network *Network) error {
	path := fmt.Sprintf("/net/%s", network.ID)
	reader, err := driver.plugin.Call("DELETE", path, nil)
	defer reader.Close()
	return err
}

func (driver *netDriver) Plug(network *Network, endpoint *Endpoint) (*execdriver.NetworkInterface, error) {
	path := fmt.Sprintf("/net/%s/", network.ID)
	reader, err := driver.plugin.Call("POST", path, endpoint)
	defer reader.Close()
	if err != nil {
		return nil, err
	}

	var iface execdriver.NetworkInterface
	return &iface, json.NewDecoder(reader).Decode(&iface)
}

func (driver *netDriver) Unplug(network *Network, endpoint *Endpoint) error {
	path := fmt.Sprintf("/net/%s/%s", network.ID, endpoint.ID)
	reader, err := driver.plugin.Call("DELETE", path, nil)
	defer reader.Close()
	return err
}

func registerNet(name string, plugin *plugins.Plugin) error {
	RegisterNetworkDriver(name, &netDriver{plugin: plugin})
	return nil
}

func init() {
	plugins.Repo.AddType("net", registerNet)
}
