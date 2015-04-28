package client

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/opts"
	flag "github.com/docker/docker/pkg/mflag"
	"github.com/docker/docker/runconfig"
)

// CmdNetCreate creates a new network.
//
// Usage: docker net create [OPTIONS] CONTAINER
func (cli *DockerCli) CmdNetConfigure(args ...string) error {
	var (
		cmd    = cli.Subcmd("net configure", "DRIVER", "Create a network driver", true)
		labels = opts.NewListOpts(opts.ValidateEnv)
	)
	cmd.Var(&labels, []string{"l", "-label"}, "Set meta data on a container")
	cmd.Require(flag.Exact, 1)
	cmd.ParseFlags(args, true)

	values := make(map[string]interface{})
	values["Labels"] = runconfig.ConvertKVStringsToMap(labels.GetAll())
	values["Driver"] = cmd.Arg(0)
	_, _, err := cli.call("POST", "/networks/configure", values, nil)
	return err
}

// CmdNetCreate creates a new network.
//
// Usage: docker net create [OPTIONS] CONTAINER
func (cli *DockerCli) CmdNetCreate(args ...string) error {
	var (
		cmd    = cli.Subcmd("net create", "", "Create a new network", true)
		driver = cmd.String([]string{"-driver"}, "", "Use driver for network")
		name   = cmd.String([]string{"-name"}, "", "Assign a name to the network")
		labels = opts.NewListOpts(opts.ValidateEnv)
	)
	cmd.Var(&labels, []string{"l", "-label"}, "Set meta data on a container")
	cmd.Require(flag.Exact, 0)
	cmd.ParseFlags(args, true)

	values := make(map[string]interface{})
	values["Labels"] = runconfig.ConvertKVStringsToMap(labels.GetAll())
	values["Name"] = *name
	values["Driver"] = *driver

	stream, _, err := cli.call("POST", "/networks/create", values, nil)
	if err != nil {
		return err
	}

	var response types.NetworkResponse
	if err := json.NewDecoder(stream).Decode(&response); err != nil {
		return err
	}

	fmt.Fprintf(cli.out, "%s\n", response.ID)
	return nil
}

// CmdNetList outputs a list of networks
//
// Usage: docker net list [OPTIONS]
func (cli *DockerCli) CmdNetList(args ...string) error {
	var (
		err error
		cmd = cli.Subcmd("net list", "", "List networks", true)
	)
	cmd.Require(flag.Exact, 0)
	cmd.ParseFlags(args, true)

	rdr, _, err := cli.call("GET", "/networks/json", nil, nil)
	if err != nil {
		return err
	}

	networks := []types.NetworkResponse{}
	if err := json.NewDecoder(rdr).Decode(&networks); err != nil {
		return err
	}

	w := tabwriter.NewWriter(cli.out, 20, 1, 3, ' ', 0)
	fmt.Fprint(w, "NETWORK ID\tNAME\tDRIVER\tLABELS\n")

	for _, net := range networks {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", net.ID, net.Name, net.Driver, net.Labels)
	}

	w.Flush()
	return nil
}

// CmdNetRm removes network with given name
//
// Usage: docker net rm [OPTIONS] name [IMAGE...]
func (cli *DockerCli) CmdNetRm(args ...string) error {
	var (
		cmd = cli.Subcmd("net rm", "NAME", "Remove one or more images", true)
	)
	cmd.Require(flag.Min, 1)
	cmd.ParseFlags(args, true)

	name := cmd.Arg(0)
	_, _, err := cli.call("DELETE", "/networks/"+name, nil, nil)
	if err != nil {
		return err
	}

	return nil
}

// CmdNetPlug creates a new endpoint for container on network
//
// Usage: docker net plug [OPTIONS] container network
func (cli *DockerCli) CmdNetPlug(args ...string) error {
	var (
		cmd    = cli.Subcmd("net plug", "CONTAINER NETWORK", "Attach a container to a network", true)
		labels = opts.NewListOpts(opts.ValidateEnv)
	)
	cmd.Var(&labels, []string{"l", "-label"}, "Set meta data on a container")
	cmd.Require(flag.Min, 2)
	cmd.ParseFlags(args, true)

	container := cmd.Arg(0)
	network := cmd.Arg(1)
	values := make(map[string]interface{})
	values["Labels"] = runconfig.ConvertKVStringsToMap(labels.GetAll())

	stream, _, err := cli.call("POST", fmt.Sprintf("/container/%s/plug/%s", container, network), values, nil)
	if err != nil {
		return err
	}

	var response types.NetworkPlugResponse
	if err := json.NewDecoder(stream).Decode(&response); err != nil {
		return err
	}
	fmt.Fprintf(cli.out, "%s\n", response.ID)
	return nil
}

// CmdNetUnplug destries said endpoint
//
// Usage: docker net attach [OPTIONS] container network
func (cli *DockerCli) CmdNetUnplug(args ...string) error {
	cmd := cli.Subcmd("net unplug", "CONTAINER ENDPOINT", "Detach endpoint on container", true)
	cmd.Require(flag.Min, 2)
	cmd.ParseFlags(args, true)

	container := cmd.Arg(0)
	endpoint := cmd.Arg(1)

	_, _, err := cli.call("POST", fmt.Sprintf("/container/%s/unplug/%s", container, endpoint), nil, nil)
	if err != nil {
		return err
	}

	return nil
}
