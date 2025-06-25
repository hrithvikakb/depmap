//go:build linux
// +build linux

package main

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

type NetConf struct {
	types.NetConf
	Bridge string `json:"bridge"`
	MTU    int    `json:"mtu"`
}

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

func loadNetConf(bytes []byte) (*NetConf, error) {
	n := &NetConf{
		MTU: 1500, // default MTU
	}
	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}
	return n, nil
}

func setupBridge(n *NetConf) (*netlink.Bridge, error) {
	// Create bridge if it doesn't exist
	br, err := netlink.LinkByName(n.Bridge)
	if err != nil {
		br = &netlink.Bridge{
			LinkAttrs: netlink.LinkAttrs{
				Name: n.Bridge,
				MTU:  n.MTU,
			},
		}
		if err := netlink.LinkAdd(br); err != nil {
			return nil, fmt.Errorf("failed to create bridge %q: %v", n.Bridge, err)
		}
	}

	// Set bridge up
	if err := netlink.LinkSetUp(br); err != nil {
		return nil, fmt.Errorf("failed to set bridge up: %v", err)
	}

	return br.(*netlink.Bridge), nil
}

func cmdAdd(args *skel.CmdArgs) error {
	n, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	// Create the bridge
	br, err := setupBridge(n)
	if err != nil {
		return err
	}

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	// Create veth pair
	hostInterface, containerInterface, err := ip.SetupVeth(args.IfName, n.MTU, "", netns)
	if err != nil {
		return err
	}

	// Get host veth link
	hostVeth, err := netlink.LinkByName(hostInterface.Name)
	if err != nil {
		return fmt.Errorf("failed to lookup host veth %q: %v", hostInterface.Name, err)
	}

	// Connect host veth end to bridge
	if err := netlink.LinkSetMaster(hostVeth, br); err != nil {
		return fmt.Errorf("failed to connect %q to bridge %v: %v", hostVeth.Attrs().Name, br.Attrs().Name, err)
	}

	// Run the IPAM plugin
	r, err := ipam.ExecAdd(n.IPAM.Type, args.StdinData)
	if err != nil {
		return err
	}

	// Convert whatever the IPAM result was into the current Result type
	result, err := current.NewResultFromResult(r)
	if err != nil {
		return err
	}

	if len(result.IPs) == 0 {
		return fmt.Errorf("IPAM plugin returned missing IP config")
	}

	// Configure the container's interface
	if err := netns.Do(func(_ ns.NetNS) error {
		if err := ipam.ConfigureIface(args.IfName, result); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	result.Interfaces = []*current.Interface{
		{
			Name:    args.IfName,
			Mac:     containerInterface.HardwareAddr.String(),
			Sandbox: args.Netns,
		},
		{
			Name: hostInterface.Name,
			Mac:  hostInterface.HardwareAddr.String(),
		},
		{
			Name: br.Attrs().Name,
			Mac:  br.Attrs().HardwareAddr.String(),
		},
	}

	return types.PrintResult(result, n.CNIVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	n, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	if err := ipam.ExecDel(n.IPAM.Type, args.StdinData); err != nil {
		return err
	}

	if args.Netns == "" {
		return nil
	}

	// Delete the interface
	if err := ns.WithNetNSPath(args.Netns, func(_ ns.NetNS) error {
		if err := ip.DelLinkByName(args.IfName); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	return nil
}

func cmdCheck(args *skel.CmdArgs) error {
	return nil
}

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.All, "hubble-cni")
}
