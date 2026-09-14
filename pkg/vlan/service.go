// Copyright 2026 Authors of spidernet-io
// SPDX-License-Identifier: Apache-2.0
package vlan

import (
	"fmt"
	"net"
	"time"

	"github.com/containernetworking/cni/pkg/skel"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"

	"github.com/spidernet-io/spiderpool/api/v1/agent/client/daemonset"
	"github.com/spidernet-io/spiderpool/api/v1/agent/models"
	spiderpoolopenapi "github.com/spidernet-io/spiderpool/pkg/openapi"

	"github.com/spidernet-io/eni-vlan/pkg/config"
	"github.com/spidernet-io/eni-vlan/pkg/networking"
)

var unixSocketPath = "/var/run/spidernet/spiderpool.sock"

// parseCNIArgs extracts K8S_POD_NAME and K8S_POD_NAMESPACE from CNI_ARGS
func parseCNIArgs(cniArgs string) (podName, podNamespace string, err error) {
	pairs := splitArgs(cniArgs)
	for _, pair := range pairs {
		kv := splitKV(pair)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "K8S_POD_NAME":
			podName = kv[1]
		case "K8S_POD_NAMESPACE":
			podNamespace = kv[1]
		}
	}
	if podName == "" || podNamespace == "" {
		return "", "", fmt.Errorf("K8S_POD_NAME and K8S_POD_NAMESPACE are required in CNI_ARGS")
	}
	return podName, podNamespace, nil
}

func splitArgs(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

func splitKV(s string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

// CmdAdd handles the CNI ADD command.
// Flow: IPAM alloc → GetWorkloadEndpoint (VLAN/MAC) → CreateVlan (real MAC, link up,
// no IP yet) → connectivity validation (ARP probe to gateway) → ConfigureIface.
func CmdAdd(args *skel.CmdArgs, n *config.NetConf) (*current.Result, error) {
	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return nil, fmt.Errorf("failed to open netns %q: %w", args.Netns, err)
	}
	defer func() {
		_ = netns.Close()
	}()

	// Step 1: Parse K8S_POD_NAME and K8S_POD_NAMESPACE from CNI_ARGS
	podName, podNamespace, err := parseCNIArgs(args.Args)
	if err != nil {
		return nil, err
	}

	// Step 2: Invoke IPAM to allocate IP
	r, err := ipam.ExecAdd(n.IPAM.Type, args.StdinData)
	if err != nil {
		return nil, fmt.Errorf("IPAM failed: %w", err)
	}

	// Any failure past this point must roll back the IPAM allocation (fail-closed).
	rollbackIPAM := func() {
		_ = ipam.ExecDel(n.IPAM.Type, args.StdinData)
	}

	result, err := current.NewResultFromResult(r)
	if err != nil {
		rollbackIPAM()
		return nil, err
	}
	if len(result.IPs) == 0 {
		rollbackIPAM()
		return nil, fmt.Errorf("IPAM returned no IP configuration")
	}

	// Step 3: Query spiderpool-agent via Unix socket to get VLAN ID and MAC
	// Using the same pattern as spiderpool IPAM: openapi.NewAgentOpenAPIUnixClient
	client, err := spiderpoolopenapi.NewAgentOpenAPIUnixClient(unixSocketPath)
	if err != nil {
		rollbackIPAM()
		return nil, fmt.Errorf("failed to create spiderpool-agent client: %w", err)
	}

	params := daemonset.NewGetWorkloadendpointParams()
	params.PodName = podName
	params.PodNamespace = podNamespace

	resp, err := client.Daemonset.GetWorkloadendpoint(params)
	if err != nil {
		rollbackIPAM()
		return nil, fmt.Errorf("GetWorkloadendpoint failed: %w", err)
	}

	// Step 4: Find the interface detail for the requested NIC
	ifaceDetail := findInterface(resp.Payload.Interfaces, args.IfName)
	if ifaceDetail == nil {
		rollbackIPAM()
		return nil, fmt.Errorf("no assignment for nic %q in spiderpool-agent response", args.IfName)
	}

	// Step 5: Create VLAN sub-interface using VLAN ID and MAC from spiderpool-agent
	mtu := n.MTU
	if mtu == 0 {
		mtu, _ = GetMTU(n.Master)
	}

	vlanIf, err := CreateVlan(n.Master, args.IfName, netns, int(ifaceDetail.Vlan), mtu, ifaceDetail.Mac)
	if err != nil {
		rollbackIPAM()
		return nil, fmt.Errorf("failed to create VLAN: %w", err)
	}

	rollback := func() {
		rollbackIPAM()
		_ = DeleteVlan(args.IfName, netns)
	}

	// Step 6: Connectivity validation — the only safe window is after the VLAN
	// sub-interface exists with its real MAC and is up, but before any IP is
	// configured (a configured IP would make the kernel passively answer ARP and
	// pollute fabric neighbor tables).
	if n.ConnectivityCheckEnabled() {
		if err := validateConnectivity(args.IfName, netns, result, n); err != nil {
			rollback()
			return nil, err
		}
	}

	// Step 7: Configure IPs (from IPAM result) on the VLAN interface
	for _, ipc := range result.IPs {
		ipc.Interface = current.Int(0)
	}
	result.Interfaces = []*current.Interface{vlanIf}
	result.DNS = n.DNS

	if err := netns.Do(func(_ ns.NetNS) error {
		return ipam.ConfigureIface(args.IfName, result)
	}); err != nil {
		rollback()
		return nil, fmt.Errorf("failed to configure IP: %w", err)
	}

	return result, nil
}

// validateConnectivity performs the final pre-flight validation of the
// cloud-assigned IP/VLAN/MAC triple: inside the pod netns it brings the VLAN
// sub-interface up (still without any IP) and ARP-probes the gateway with
// sender IP = allocated IP. A reply proves the cloud configuration is live;
// no reply means wrong VLAN / MAC not effective / IP-MAC binding not pushed,
// and the CNI ADD must fail closed.
//
// IPv4 only for now; IPv6 (NS/NA) is a TODO.
func validateConnectivity(ifName string, netns ns.NetNS, result *current.Result, n *config.NetConf) error {
	var srcIP, gwIP net.IP
	for _, ipc := range result.IPs {
		if ipc.Address.IP.To4() != nil && ipc.Gateway != nil && ipc.Gateway.To4() != nil {
			srcIP = ipc.Address.IP.To4()
			gwIP = ipc.Gateway.To4()
			break
		}
	}
	if srcIP == nil {
		// No IPv4 address with gateway: nothing to probe. TODO: IPv6 NS/NA probing.
		return nil
	}

	timeout := time.Duration(n.CheckTimeoutMs) * time.Millisecond
	return netns.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("failed to find %q for connectivity validation: %w", ifName, err)
		}
		if err := netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("failed to set %q up for connectivity validation: %w", ifName, err)
		}
		return networking.CheckGatewayReachable(ifName, srcIP, gwIP, n.CheckRetries, timeout)
	})
}

// findInterface finds the interface detail by name from the list
func findInterface(interfaces []*models.InterfaceDetail, ifName string) *models.InterfaceDetail {
	for _, iface := range interfaces {
		if iface.Interface != nil && *iface.Interface == ifName {
			return iface
		}
	}
	return nil
}
