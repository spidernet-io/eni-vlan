// Copyright 2026 Authors of spidernet-io
// SPDX-License-Identifier: Apache-2.0

// Package networking implements the pre-flight validation of the
// cloud-assigned IP/VLAN/MAC triple.
//
// It reuses spiderpool's networking helpers instead of maintaining a second
// packet implementation:
//   - sending: spiderpool pkg/networking/networking SendARPReuqest
//     (AF_PACKET/SOCK_DGRAM, the kernel builds the Ethernet header, so the
//     source MAC is automatically the real MAC of the probing interface)
//   - receiving: github.com/mdlayher/arp client, the same library used by
//     spiderpool's Detector in pkg/networking/networking/ipam_detection.go
//
// This matters on IaaS fabrics with strict anti-spoofing: only packets with
// source IP = bound IP, source MAC = sub-ENI MAC and the correct VLAN are
// forwarded, so an ARP reply from the gateway proves the whole cloud
// configuration is live.
package networking

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mdlayher/arp"
	"github.com/vishvananda/netlink"

	spidernetworking "github.com/spidernet-io/spiderpool/pkg/networking/networking"
)

// IsGatewayReply reports whether pkt is an ARP reply sent by gatewayIP.
func IsGatewayReply(pkt *arp.Packet, gatewayIP net.IP) bool {
	return pkt != nil && pkt.Operation == arp.OperationReply && pkt.SenderIP.Equal(gatewayIP.To4())
}

// CheckGatewayReachable performs the final connectivity validation of the
// cloud-assigned IP/VLAN/MAC triple: it sends ARP requests
// (sender IP = allocated IP, sender MAC = interface real MAC, target = gateway)
// on ifName and waits for a reply from the gateway.
//
// It MUST be called inside the pod network namespace, after the VLAN
// sub-interface is up but strictly BEFORE any IP address is configured on it —
// otherwise the kernel's passive ARP replies would pollute neighbor tables on
// the fabric (see spiderpool #4582/#4588).
//
// The retry/receive loop mirrors spiderpool's Detector.detectGateway4Reachable,
// with configurable retries and per-attempt timeout.
//
// TODO: IPv6 support via NS/NA probing.
func CheckGatewayReachable(ifName string, senderIP, gatewayIP net.IP, retries int, timeout time.Duration) error {
	if senderIP.To4() == nil || gatewayIP.To4() == nil {
		return fmt.Errorf("connectivity validation requires IPv4 sender/gateway addresses, got sender %q gateway %q", senderIP, gatewayIP)
	}

	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("failed to find interface %q: %w", ifName, err)
	}
	ifi, err := net.InterfaceByName(ifName)
	if err != nil {
		return fmt.Errorf("failed to find interface %q: %w", ifName, err)
	}

	arpClient, err := arp.Dial(ifi)
	if err != nil {
		return fmt.Errorf("failed to init arp client on %q: %w", ifName, err)
	}
	defer func() { _ = arpClient.Close() }()

	// Overall guard against edge cases where the read deadline does not take
	// effect (same rationale as spiderpool's Detector).
	ctx, cancel := context.WithTimeout(context.Background(), timeout*time.Duration(retries)*2)
	defer cancel()

	for i := 0; i < retries; i++ {
		if err := arpClient.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return fmt.Errorf("failed to set read deadline: %w", err)
		}

		if err := spidernetworking.SendARPReuqest(link, senderIP, gatewayIP); err != nil {
			return fmt.Errorf("failed to send ARP request on %q: %w", ifName, err)
		}

		for {
			select {
			case <-ctx.Done():
				return fmt.Errorf("connectivity validation exceeded the max timeout: %w", ctx.Err())
			default:
			}

			pkt, _, err := arpClient.Read()
			if err != nil {
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					break // per-attempt timeout, retry
				}
				return fmt.Errorf("failed to receive ARP packet on %q: %w", ifName, err)
			}
			if IsGatewayReply(pkt, gatewayIP) {
				return nil
			}
			// unrelated packet, keep reading until the deadline
		}
	}

	return fmt.Errorf("connectivity validation failed: no ARP reply from gateway %s on %q after %d attempts (%s each); the cloud-assigned IP/VLAN/MAC configuration is likely wrong or not yet effective",
		gatewayIP, ifName, retries, timeout)
}
