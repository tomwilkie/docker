package simplebridge

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon/execdriver"
	"github.com/docker/docker/daemon/networkdriver"
	"github.com/docker/docker/daemon/networkdriver/ipallocator"
	"github.com/docker/docker/pkg/iptables"
	"github.com/docker/docker/pkg/parsers/kernel"
	"github.com/docker/docker/pkg/resolvconf"
	"github.com/docker/libcontainer/netlink"
)

// TODO
// - portmapping
// - nat
// - finish ipv6

const (
	DefaultNetworkBridge     = "docker0"
	MaxAllocatedPortAttempts = 10
)

var (
	addrs = []string{
		// Here we don't follow the convention of using the 1st IP of the range for the gateway.
		// This is to use the same gateway IPs as the /24 ranges, which predate the /16 ranges.
		// In theory this shouldn't matter - in practice there's bound to be a few scripts relying
		// on the internal addressing or other things like that.
		// They shouldn't, but hey, let's not break them unless we really have to.
		"172.17.42.1/16", // Don't use 172.16.0.0/16, it conflicts with EC2 DNS 172.16.0.23
		"10.0.42.1/16",   // Don't even try using the entire /8, that's too intrusive
		"10.1.42.1/16",
		"10.42.42.1/16",
		"172.16.42.1/24",
		"172.16.43.1/24",
		"172.16.44.1/24",
		"10.0.42.1/24",
		"10.0.43.1/24",
		"192.168.42.1/24",
		"192.168.43.1/24",
		"192.168.44.1/24",
	}
	defaultBindingIP = net.ParseIP("0.0.0.0")
)

// Network interface represents the networking stack of a container
type networkInterface struct {
	IP           net.IP
	IPv6         net.IP
	PortMappings []net.Addr // There are mappings to the host interfaces
}

type ifaces struct {
	c map[string]*networkInterface
	sync.Mutex
}

func (i *ifaces) Set(key string, n *networkInterface) {
	i.Lock()
	i.c[key] = n
	i.Unlock()
}

func (i *ifaces) Get(key string) *networkInterface {
	i.Lock()
	res := i.c[key]
	i.Unlock()
	return res
}

type Network struct {
	bridgeIface string

	bridgeIPv4Addr    net.IP     // IP address for bridge
	bridgeIPv4Network *net.IPNet // Subnet for bridge
	fixedIPv4Subnet   *net.IPNet // Subnet for allocationl

	enableIPv6        bool
	bridgeIPv6Addr    net.IP
	bridgeIPv6Network *net.IPNet
	fixedIPv6Subnet   *net.IPNet

	enableIPTables       bool
	enableICC            bool
	enableIPMasq         bool
	enableIPForward      bool
	enableDefaultGateway bool

	currentInterfaces ifaces
	ipAllocator       *ipallocator.IPAllocator
}

type config map[string]string

func (c config) getString(name string) string {
	value, found := c[name]
	if found {
		return value
	}
	return ""
}

func (c config) getBool(name string, fedault bool) bool {
	value, found := c[name]
	bVal, err := strconv.ParseBool(value)
	if found && err != nil {
		return bVal
	}
	return fedault
}

func NewNetwork(conf config) (*Network, error) {
	var (
		bridgeIface = conf.getString("BridgeIface")

		bridgeIP  = conf.getString("BridgeIP")
		fixedCIDR = conf.getString("FixedCIDR")

		enableIPv6  = conf.getBool("EnableIPv6", false)
		bridgeIPv6  = "fe80::1/64"
		fixedCIDRv6 = conf.getString("FixedCIDRv6")

		enableIPTables       = conf.getBool("EnableIptables", false)
		enableICC            = conf.getBool("InterContainerCommunication", true)
		enableIPMasq         = conf.getBool("EnableIpMasq", false)
		enableIPForward      = conf.getBool("EnableIpForward", false)
		enableDefaultGateway = conf.getBool("EnableDefaultGateway", false)
	)

	network := &Network{
		enableIPTables:       enableIPTables,
		enableICC:            enableICC,
		enableIPMasq:         enableIPMasq,
		enableIPForward:      enableIPForward,
		enableDefaultGateway: enableDefaultGateway,

		currentInterfaces: ifaces{c: make(map[string]*networkInterface)},
		ipAllocator:       ipallocator.New(),
	}

	if bridgeIface == "" {
		bridgeIface, err := findFreeBridgeName()
		if err != nil {
			return nil, err
		}
		network.bridgeIface = bridgeIface
	} else {
		network.bridgeIface = bridgeIface
	}

	if bridgeIP != "" {
		ipv4Addr, ipv4Net, err := net.ParseCIDR(bridgeIP)
		if err != nil {
			return nil, err
		}
		network.bridgeIPv4Addr = ipv4Addr
		network.bridgeIPv4Network = ipv4Net
	} else {
		ipv4Addr, ipv4Net, err := findFreeIp()
		if err != nil {
			return nil, err
		}
		network.bridgeIPv4Addr = ipv4Addr
		network.bridgeIPv4Network = ipv4Net
	}

	// TODO Should this be ignored if bridge ip is not set?
	if fixedCIDR != "" {
		_, subnet, err := net.ParseCIDR(fixedCIDR)
		if err != nil {
			return nil, err
		}
		network.fixedIPv4Subnet = subnet
	}

	if enableIPv6 {
		ipv6Addr, ipv6Net, err := net.ParseCIDR(bridgeIPv6)
		if err != nil {
			return nil, err
		}
		network.enableIPv6 = true
		network.bridgeIPv6Addr = ipv6Addr
		network.bridgeIPv6Network = ipv6Net

		if fixedCIDRv6 != "" {
			_, subnet, err := net.ParseCIDR(fixedCIDRv6)
			if err != nil {
				return nil, err
			}
			network.fixedIPv6Subnet = subnet
		}
	}

	return network, nil
}

func findFreeIp() (net.IP, *net.IPNet, error) {
	nameservers := []string{}
	resolvConf, _ := resolvconf.Get()
	// We don't check for an error here, because we don't really care
	// if we can't read /etc/resolv.conf. So instead we skip the append
	// if resolvConf is nil. It either doesn't exist, or we can't read it
	// for some reason.
	if resolvConf != nil {
		nameservers = append(nameservers, resolvconf.GetNameserversAsCIDR(resolvConf)...)
	}

	for _, cidr := range addrs {
		ipaddress, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, nil, err
		}
		if err := networkdriver.CheckNameserverOverlaps(nameservers, network); err == nil {
			if err := networkdriver.CheckRouteOverlaps(network); err == nil {
				return ipaddress, network, nil
			}
		}
	}

	return nil, nil, fmt.Errorf("Could not find a free IP address range.")
}

func findFreeBridgeName() (string, error) {
	template := "docker%d"

	for i := 0; i < 10; i++ {
		name := fmt.Sprintf(template, i)
		_, _, err := networkdriver.GetIfaceAddr(name)
		if err != nil {
			return name, nil
		}
	}

	return "", fmt.Errorf("Cannot find free bridge name")
}

func (n Network) Setup() error {
	// In this function we assume all the fields have been parsed and are populated.
	// All we want to do is try and make the real world match the config, and fail
	// if thats not possible.

	// Create the bridge is it doesn't exist
	_, _, err := networkdriver.GetIfaceAddr(n.bridgeIface)
	if err != nil {
		if err := n.configureBridge(); err != nil {
			return err
		}

		_, _, err := networkdriver.GetIfaceAddr(n.bridgeIface)
		if err != nil {
			return err
		}

		if n.fixedIPv6Subnet != nil {
			// Setting route to global IPv6 subnet
			logrus.Infof("Adding route to IPv6 network %q via device %q", n.fixedIPv6Subnet, n.bridgeIface)
			if err := netlink.AddRoute(n.fixedIPv6Subnet.String(), "", "", n.bridgeIface); err != nil {
				logrus.Fatalf("Could not add route to IPv6 network %q via device %q",
					n.fixedIPv6Subnet, n.bridgeIface)
			}
		}
	}

	// Validate that the bridge ipv4 address matches that specified
	{
		addrv4, _, err := networkdriver.GetIfaceAddr(n.bridgeIface)
		if err != nil {
			return err
		}
		netv4 := addrv4.(*net.IPNet)
		if !netv4.IP.Equal(n.bridgeIPv4Addr) {
			return fmt.Errorf("Bridge ip (%s) does not match existing bridge configuration %s",
				addrv4, n.bridgeIPv4Addr)
		}
	}

	// A bridge might exist but not have any IPv6 addr associated with it yet
	// (for example, an existing Docker installation that has only been used
	// with IPv4 and docker0 already is set up) In that case, we can perform
	// the bridge init for IPv6 here, else we will error out below if --ipv6=true
	if n.enableIPv6 {
		_, addrsv6, err := networkdriver.GetIfaceAddr(n.bridgeIface)
		if err != nil {
			return err
		}
		if len(addrsv6) == 0 {
			if err := n.setupIPv6Bridge(); err != nil {
				return err
			}
		}

		// Recheck addresses now that IPv6 is setup on the bridge
		_, addrsv6, err = networkdriver.GetIfaceAddr(n.bridgeIface)
		if err != nil {
			return err
		}

		// Validate that the bridge ipv6 address matches that specified
		found := false
		for _, addrv6 := range addrsv6 {
			addrv6 := addrv6.(*net.IPNet)
			if addrv6.IP.Equal(n.bridgeIPv6Addr) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Bridge IPv6 does not match existing bridge configuration %s", n.bridgeIPv6Addr)
		}
		if len(addrsv6) == 0 {
			return errors.New("IPv6 enabled but no IPv6 detected")
		}
	}

	// Configure iptables for link support
	if n.enableIPTables {
		if err := n.setupIPTables(); err != nil {
			return err
		}
	}

	// Enable IPv4 forwarding
	if n.enableIPForward {
		if err := ioutil.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte{'1', '\n'}, 0644); err != nil {
			logrus.Warnf("WARNING: unable to enable IPv4 forwarding: %s\n", err)
		}

		if n.fixedIPv6Subnet != nil {
			// Enable IPv6 forwarding
			if err := ioutil.WriteFile("/proc/sys/net/ipv6/conf/default/forwarding", []byte{'1', '\n'}, 0644); err != nil {
				logrus.Warnf("WARNING: unable to enable IPv6 default forwarding: %s\n", err)
			}
			if err := ioutil.WriteFile("/proc/sys/net/ipv6/conf/all/forwarding", []byte{'1', '\n'}, 0644); err != nil {
				logrus.Warnf("WARNING: unable to enable IPv6 all forwarding: %s\n", err)
			}
		}
	}

	// We can always try removing the iptables
	if err := iptables.RemoveExistingChain("DOCKER", iptables.Nat); err != nil {
		return err
	}

	if n.enableIPTables {
		_, err := iptables.NewChain("DOCKER", n.bridgeIface, iptables.Nat)
		if err != nil {
			return err
		}
		_, err = iptables.NewChain("DOCKER", n.bridgeIface, iptables.Filter)
		if err != nil {
			return err
		}
		// TODO portMapper.SetIptablesChain(chain)
	}

	if n.fixedIPv4Subnet != nil {
		logrus.Debugf("Subnet: %v", n.fixedIPv4Subnet)
		if err := n.ipAllocator.RegisterSubnet(n.bridgeIPv4Network, n.fixedIPv4Subnet); err != nil {
			return err
		}
	}

	if n.fixedIPv6Subnet != nil {
		logrus.Debugf("Subnet: %v", n.fixedIPv6Subnet)
		if err := n.ipAllocator.RegisterSubnet(n.fixedIPv6Subnet, n.fixedIPv6Subnet); err != nil {
			return err
		}
	}

	// Block BridgeIP in IP allocator
	n.ipAllocator.RequestIP(n.bridgeIPv4Network, n.bridgeIPv4Network.IP)

	// https://github.com/docker/docker/issues/2768
	//job.Eng.HackSetGlobalVar("httpapi.bridgeIP", n.bridgeIPv4Network.IP)

	return nil
}

func (n Network) setupIPTables() error {
	// Enable NAT

	if n.enableIPMasq {
		natArgs := []string{"-s", n.bridgeIPv4Addr.String(), "!", "-o", n.bridgeIface, "-j", "MASQUERADE"}

		if !iptables.Exists(iptables.Nat, "POSTROUTING", natArgs...) {
			if output, err := iptables.Raw(append([]string{
				"-t", string(iptables.Nat), "-I", "POSTROUTING"}, natArgs...)...); err != nil {
				return fmt.Errorf("Unable to enable network bridge NAT: %s", err)
			} else if len(output) != 0 {
				return &iptables.ChainError{Chain: "POSTROUTING", Output: output}
			}
		}
	}

	var (
		args       = []string{"-i", n.bridgeIface, "-o", n.bridgeIface, "-j"}
		acceptArgs = append(args, "ACCEPT")
		dropArgs   = append(args, "DROP")
	)

	if !n.enableICC {
		iptables.Raw(append([]string{"-D", "FORWARD"}, acceptArgs...)...)

		if !iptables.Exists(iptables.Filter, "FORWARD", dropArgs...) {
			logrus.Debugf("Disable inter-container communication")
			if output, err := iptables.Raw(append([]string{"-I", "FORWARD"}, dropArgs...)...); err != nil {
				return fmt.Errorf("Unable to prevent intercontainer communication: %s", err)
			} else if len(output) != 0 {
				return fmt.Errorf("Error disabling intercontainer communication: %s", output)
			}
		}
	} else {
		iptables.Raw(append([]string{"-D", "FORWARD"}, dropArgs...)...)

		if !iptables.Exists(iptables.Filter, "FORWARD", acceptArgs...) {
			logrus.Debugf("Enable inter-container communication")
			if output, err := iptables.Raw(append([]string{"-I", "FORWARD"}, acceptArgs...)...); err != nil {
				return fmt.Errorf("Unable to allow intercontainer communication: %s", err)
			} else if len(output) != 0 {
				return fmt.Errorf("Error enabling intercontainer communication: %s", output)
			}
		}
	}

	// Accept all non-intercontainer outgoing packets
	outgoingArgs := []string{"-i", n.bridgeIface, "!", "-o", n.bridgeIface, "-j", "ACCEPT"}
	if !iptables.Exists(iptables.Filter, "FORWARD", outgoingArgs...) {
		if output, err := iptables.Raw(append([]string{"-I", "FORWARD"}, outgoingArgs...)...); err != nil {
			return fmt.Errorf("Unable to allow outgoing packets: %s", err)
		} else if len(output) != 0 {
			return &iptables.ChainError{Chain: "FORWARD outgoing", Output: output}
		}
	}

	// Accept incoming packets for existing connections
	existingArgs := []string{"-o", n.bridgeIface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"}

	if !iptables.Exists(iptables.Filter, "FORWARD", existingArgs...) {
		if output, err := iptables.Raw(append([]string{"-I", "FORWARD"}, existingArgs...)...); err != nil {
			return fmt.Errorf("Unable to allow incoming packets: %s", err)
		} else if len(output) != 0 {
			return &iptables.ChainError{Chain: "FORWARD incoming", Output: output}
		}
	}
	return nil
}

// configureBridge attempts to create and configure a network bridge interface named `bridgeIface` on the host
// If bridgeIP is empty, it will try to find a non-conflicting IP from the Docker-specified private ranges
// If the bridge `bridgeIface` already exists, it will only perform the IP address association with the existing
// bridge (fixes issue #8444)
// If an address which doesn't conflict with existing interfaces can't be found, an error is returned.
func (n Network) configureBridge() error {
	logrus.Debugf("Creating bridge %s with network %s", n.bridgeIface, n.bridgeIPv4Addr)

	if err := createBridgeIface(n.bridgeIface); err != nil {
		// The bridge may already exist, therefore we can ignore an "exists" error
		if !os.IsExist(err) {
			return err
		}
	}

	iface, err := net.InterfaceByName(n.bridgeIface)
	if err != nil {
		return err
	}

	if err := netlink.NetworkLinkAddIp(iface, n.bridgeIPv4Addr, n.bridgeIPv4Network); err != nil {
		return fmt.Errorf("Unable to add private network: %s", err)
	}

	if n.enableIPv6 {
		if err := n.setupIPv6Bridge(); err != nil {
			return err
		}
	}

	if err := netlink.NetworkLinkUp(iface); err != nil {
		return fmt.Errorf("Unable to start network bridge: %s", err)
	}
	return nil
}

func (n Network) setupIPv6Bridge() error {
	iface, err := net.InterfaceByName(n.bridgeIface)
	if err != nil {
		return err
	}

	// Enable IPv6 on the bridge
	procFile := "/proc/sys/net/ipv6/conf/" + iface.Name + "/disable_ipv6"
	if err := ioutil.WriteFile(procFile, []byte{'0', '\n'}, 0644); err != nil {
		return fmt.Errorf("Unable to enable IPv6 addresses on bridge: %v", err)
	}

	if err := netlink.NetworkLinkAddIp(iface, n.bridgeIPv6Addr, n.bridgeIPv6Network); err != nil {
		return fmt.Errorf("Unable to add private IPv6 network: %v", err)
	}

	return nil
}

func createBridgeIface(name string) error {
	kv, err := kernel.GetKernelVersion()
	// Only set the bridge's mac address if the kernel version is > 3.3
	// before that it was not supported
	setBridgeMacAddr := err == nil && (kv.Kernel >= 3 && kv.Major >= 3)
	logrus.Debugf("setting bridge mac address = %v", setBridgeMacAddr)
	return netlink.CreateBridge(name, setBridgeMacAddr)
}

// Generate a IEEE802 compliant MAC address from the given IP address.
//
// The generator is guaranteed to be consistent: the same IP will always yield the same
// MAC address. This is to avoid ARP cache issues.
func generateMacAddr(ip net.IP) net.HardwareAddr {
	hw := make(net.HardwareAddr, 6)

	// The first byte of the MAC address has to comply with these rules:
	// 1. Unicast: Set the least-significant bit to 0.
	// 2. Address is locally administered: Set the second-least-significant bit (U/L) to 1.
	// 3. As "small" as possible: The veth address has to be "smaller" than the bridge address.
	hw[0] = 0x02

	// The first 24 bits of the MAC represent the Organizationally Unique Identifier (OUI).
	// Since this address is locally administered, we can do whatever we want as long as
	// it doesn't conflict with other addresses.
	hw[1] = 0x42

	// Insert the IP address into the last 32 bits of the MAC address.
	// This is a simple way to guarantee the address will be consistent and unique.
	copy(hw[2:], ip.To4())

	return hw
}

func linkLocalIPv6FromMac(mac string) (string, error) {
	hx := strings.Replace(mac, ":", "", -1)
	hw, err := hex.DecodeString(hx)
	if err != nil {
		return "", errors.New("Could not parse MAC address " + mac)
	}

	hw[0] ^= 0x2

	return fmt.Sprintf("fe80::%x%x:%xff:fe%x:%x%x/64", hw[0], hw[1], hw[2], hw[3], hw[4], hw[5]), nil
}

// Allocate a network interface
func (n *Network) Allocate(id string, conf config) (*execdriver.NetworkInterface, error) {
	var (
		err error

		requestedIP = net.ParseIP(conf.getString("RequestedIP"))
		ip          net.IP

		requestedMAC = conf.getString("RequestedMac")
		mac          net.HardwareAddr

		//id            = job.Args[0]
		//requestedIPv6 = net.ParseIP(job.Getenv("RequestedIPv6"))
		//globalIPv6    net.IP
	)

	ip, err = n.ipAllocator.RequestIP(n.bridgeIPv4Network, requestedIP)
	if err != nil {
		logrus.Errorf("2: %s", err)
		return nil, err
	}

	if requestedMAC == "" {
		mac = generateMacAddr(ip)
	} else {
		mac, err = net.ParseMAC(requestedMAC)
		if err != nil {
			logrus.Errorf("1: %s", err)
			return nil, err
		}
	}

	// NB you can only have one default gateway in a container; you
	// won't be able to start a container if this is specified on two networks!
	var gateway string
	if n.enableDefaultGateway {
		gateway = n.bridgeIPv4Addr.String()
	}

	//if globalIPv6Network != nil {
	//	// If globalIPv6Network Size is at least a /80 subnet generate IPv6 address from MAC address
	//	netmaskOnes, _ := globalIPv6Network.Mask.Size()
	//	if requestedIPv6 == nil && netmaskOnes <= 80 {
	//		requestedIPv6 = make(net.IP, len(globalIPv6Network.IP))
	//		copy(requestedIPv6, globalIPv6Network.IP)
	//		for i, h := range mac {
	//			requestedIPv6[i+10] = h
	//		}
	//	}
	//
	//	globalIPv6, err = ipAllocator.RequestIP(globalIPv6Network, requestedIPv6)
	//	if err != nil {
	//		logrus.Errorf("Allocator: RequestIP v6: %v", err)
	//		return err
	//	}
	//	logrus.Infof("Allocated IPv6 %s", globalIPv6)
	//}

	n.currentInterfaces.Set(id, &networkInterface{
		IP: ip,
		//IPv6: globalIPv6,
	})

	size, _ := n.bridgeIPv4Network.Mask.Size()
	return &execdriver.NetworkInterface{
		Gateway:     gateway,
		IPAddress:   ip.String(),
		IPPrefixLen: size,
		MacAddress:  mac.String(),
		Bridge:      n.bridgeIface,
	}, nil

	// If linklocal IPv6
	//localIPv6Net, err := linkLocalIPv6FromMac(mac.String())
	//if err != nil {
	//	return err
	//}
	//localIPv6, _, _ := net.ParseCIDR(localIPv6Net)
	//out.Set("LinkLocalIPv6", localIPv6.String())
	//out.Set("MacAddress", mac.String())
	//
	//if globalIPv6Network != nil {
	//	out.Set("GlobalIPv6", globalIPv6.String())
	//	sizev6, _ := globalIPv6Network.Mask.Size()
	//	out.SetInt("GlobalIPv6PrefixLen", sizev6)
	//	out.Set("IPv6Gateway", bridgeIPv6Addr.String())
	//}
}

// Release an interface for a select ip
func (n *Network) Release(id string) error {
	containerInterface := n.currentInterfaces.Get(id)
	if containerInterface == nil {
		return fmt.Errorf("No network information to release for %s", id)
	}

	//for _, nat := range containerInterface.PortMappings {
	//	if err := portMapper.Unmap(nat); err != nil {
	//		logrus.Infof("Unable to unmap port %s: %s", nat, err)
	//	}
	//}

	if err := n.ipAllocator.ReleaseIP(n.bridgeIPv4Network, containerInterface.IP); err != nil {
		logrus.Infof("Unable to release IPv4 %s", err)
	}
	//if globalIPv6Network != nil {
	//	if err := ipAllocator.ReleaseIP(globalIPv6Network, containerInterface.IPv6); err != nil {
	//		logrus.Infof("Unable to release IPv6 %s", err)
	//	}
	//}
	return nil
}

// Allocate an external port and map it to the interface
//func AllocatePort(job *engine.Job) error {
//	var (
//		err error
//
//		ip            = defaultBindingIP
//		id            = job.Args[0]
//		hostIP        = job.Getenv("HostIP")
//		hostPort      = job.GetenvInt("HostPort")
//		containerPort = job.GetenvInt("ContainerPort")
//		proto         = job.Getenv("Proto")
//		network       = currentInterfaces.Get(id)
//	)
//
//	if hostIP != "" {
//		ip = net.ParseIP(hostIP)
//		if ip == nil {
//			return fmt.Errorf("Bad parameter: invalid host ip %s", hostIP)
//		}
//	}
//
//	// host ip, proto, and host port
//	var container net.Addr
//	switch proto {
//	case "tcp":
//		container = &net.TCPAddr{IP: network.IP, Port: containerPort}
//	case "udp":
//		container = &net.UDPAddr{IP: network.IP, Port: containerPort}
//	default:
//		return fmt.Errorf("unsupported address type %s", proto)
//	}
//
//	//
//	// Try up to 10 times to get a port that's not already allocated.
//	//
//	// In the event of failure to bind, return the error that portmapper.Map
//	// yields.
//	//
//
//	var host net.Addr
//	for i := 0; i < MaxAllocatedPortAttempts; i++ {
//		if host, err = portMapper.Map(container, ip, hostPort); err == nil {
//			break
//		}
//		// There is no point in immediately retrying to map an explicitly
//		// chosen port.
//		if hostPort != 0 {
//			logrus.Warnf("Failed to allocate and map port %d: %s", hostPort, err)
//			break
//		}
//		logrus.Warnf("Failed to allocate and map port: %s, retry: %d", err, i+1)
//	}
//
//	if err != nil {
//		return err
//	}
//
//	network.PortMappings = append(network.PortMappings, host)
//
//	out := engine.Env{}
//	switch netAddr := host.(type) {
//	case *net.TCPAddr:
//		out.Set("HostIP", netAddr.IP.String())
//		out.SetInt("HostPort", netAddr.Port)
//	case *net.UDPAddr:
//		out.Set("HostIP", netAddr.IP.String())
//		out.SetInt("HostPort", netAddr.Port)
//	}
//	if _, err := out.WriteTo(job.Stdout); err != nil {
//		return err
//	}
//
//	return nil
//}
//
//func LinkContainers(job *engine.Job) error {
//	var (
//		action       = job.Args[0]
//		nfAction     iptables.Action
//		childIP      = job.Getenv("ChildIP")
//		parentIP     = job.Getenv("ParentIP")
//		ignoreErrors = job.GetenvBool("IgnoreErrors")
//		ports        = job.GetenvList("Ports")
//	)
//
//	switch action {
//	case "-A":
//		nfAction = iptables.Append
//	case "-I":
//		nfAction = iptables.Insert
//	case "-D":
//		nfAction = iptables.Delete
//	default:
//		return fmt.Errorf("Invalid action '%s' specified", action)
//	}
//
//	ip1 := net.ParseIP(parentIP)
//	if ip1 == nil {
//		return fmt.Errorf("Parent IP '%s' is invalid", parentIP)
//	}
//	ip2 := net.ParseIP(childIP)
//	if ip2 == nil {
//		return fmt.Errorf("Child IP '%s' is invalid", childIP)
//	}
//
//	chain := iptables.Chain{Name: "DOCKER", Bridge: bridgeIface}
//	for _, p := range ports {
//		port := nat.Port(p)
//		if err := chain.Link(nfAction, ip1, ip2, port.Int(), port.Proto()); !ignoreErrors && err != nil {
//			return err
//		}
//	}
//	return nil
//}
