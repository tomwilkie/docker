package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/plugins"
	"github.com/docker/libnetwork/driverapi"
)

type netLibDriver struct {
	plugin *plugins.Plugin
}


func (driver *netLibDriver) Config(config interface{}) error {
	return nil
}

func (driver *netLibDriver) CreateNetwork(nid driverapi.UUID, config interface{}) error {
	reader, err := driver.plugin.Call("POST", string(nid), config)
	if err != nil {
		logrus.Warningf("Driver returned err:", err)
		return err
	}
	reader.Close()
	return nil
}

func (driver *netLibDriver) DeleteNetwork(nid driverapi.UUID) error {
	reader, err := driver.plugin.Call("DELETE", string(nid), nil)
	if err != nil {
		logrus.Warningf("Driver returned err:", err)
		return err
	}
	reader.Close()
	return nil
}

func (driver *netLibDriver) CreateEndpoint(nid, eid driverapi.UUID, key string, config interface{}) (*driverapi.SandboxInfo, error) {
	path := fmt.Sprintf("%s/%s", nid, eid)
	reader, err := driver.plugin.Call("POST", path, config)
	if err != nil {
		logrus.Warningf("Driver returned err:", err)
		return nil, err
	}
	defer reader.Close()
	var sbinfo driverapi.SandboxInfo
	if err := json.NewDecoder(reader).Decode(&sbinfo); err != nil {
		return nil, err
	}
	return &sbinfo, nil
}

func (driver *netLibDriver) DeleteEndpoint(nid, eid driverapi.UUID) error {
	path := fmt.Sprintf("%s/%s", nid, eid)
	reader, err := driver.plugin.Call("DELETE", path, nil)
	if err != nil {
		logrus.Warningf("Driver returned err:", err)
		return err
	}
	reader.Close()
	return nil
}

func (daemon *Daemon) registerLibNet(name string, plugin *plugins.Plugin) error {
	daemon.networkCtrlr.RegisterDriver(name, &netLibDriver{plugin: plugin})
	return nil
}
