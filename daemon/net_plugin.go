package daemon

import (
	"encoding/json"
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon/execdriver"
	"github.com/docker/docker/plugins"
)

type netDriver struct {
	plugin *plugins.Plugin
}

func (driver *netDriver) Setup(network *Network) error {
	reader, err := driver.plugin.Call("POST", "", network)
	if err != nil {
		logrus.Warningf("Driver returned err:", err)
		return err
	}
	reader.Close()
	return nil
}

func (driver *netDriver) Destroy(network *Network) error {
	path := network.ID
	reader, err := driver.plugin.Call("DELETE", path, nil)
	if err != nil {
		logrus.Warningf("Driver returned err:", err)
		return err
	}
	reader.Close()
	return nil
}

func (driver *netDriver) Plug(network *Network, endpoint *Endpoint) (*execdriver.NetworkInterface, error) {
	path := network.ID + "/"
	reader, err := driver.plugin.Call("POST", path, endpoint)
	if err != nil {
		logrus.Warningf("Driver returned err:", err)
		return nil, err
	}
	defer reader.Close()
	var iface execdriver.NetworkInterface
	return &iface, json.NewDecoder(reader).Decode(&iface)
}

func (driver *netDriver) Unplug(network *Network, endpoint *Endpoint) error {
	path := fmt.Sprintf("%s/%s", network.ID, endpoint.ID)
	reader, err := driver.plugin.Call("DELETE", path, nil)
	if err != nil {
		logrus.Warningf("Driver returned err:", err)
		return err
	}
	reader.Close()
	return nil
}

func registerNet(name string, plugin *plugins.Plugin) error {
	RegisterNetworkDriver(name, &netDriver{plugin: plugin})
	return nil
}

//func init() {
//	plugins.Repo.AddType("net", registerNet)
//}
