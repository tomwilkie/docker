package daemon

import (
	"encoding/json"
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/plugins"
	"github.com/docker/libnetwork/driverapi"
	"net"
)

type iface struct {
	SrcName    string
	DstName    string
	Address    string
	MACAddress string
}

type sbInfo struct {
	Interfaces  []*iface
	Gateway     net.IP
	GatewayIPv6 net.IP
}

func (sb *sbInfo) toSandboxInfo() (*driverapi.SandboxInfo, error) {
	var (
		ifaces []*driverapi.Interface = make([]*driverapi.Interface, len(sb.Interfaces))
	)
	for i, inIf := range sb.Interfaces {
		outIf := &driverapi.Interface{
			SrcName:    inIf.SrcName,
			DstName:    inIf.DstName,
			MACAddress: inIf.MACAddress,
		}
		ip, ipnet, err := net.ParseCIDR(inIf.Address)
		if err != nil {
			return nil, err
		}
		ipnet.IP = ip
		outIf.Address = ipnet
		ifaces[i] = outIf
	}
	return &driverapi.SandboxInfo{
		Interfaces:  ifaces,
		Gateway:     nil,
		GatewayIPv6: nil,
	}, nil
}

type netLibDriver struct {
	plugin *plugins.Plugin
}

func (driver *netLibDriver) Config(config interface{}) error {
	return nil
}

func (driver *netLibDriver) CreateNetwork(nid driverapi.UUID, config interface{}) error {
	reader, err := driver.plugin.Call("PUT", string(nid), config)
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
	reader, err := driver.plugin.Call("PUT", path, config)
	if err != nil {
		logrus.Warningf("Driver returned err:", err)
		return nil, err
	}
	defer reader.Close()
	var sbinfo sbInfo
	if err := json.NewDecoder(reader).Decode(&sbinfo); err != nil {
		logrus.Warningf("Driver returned invalid JSON:", err)
		return nil, err
	}

	var sb *driverapi.SandboxInfo
	if sb, err = sbinfo.toSandboxInfo(); err != nil {
		logrus.Warningf("Unable to convert sbInfo")
		return nil, err
	}
	logrus.Infof("Plugin returned %+v", sbinfo)
	return sb, nil
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
