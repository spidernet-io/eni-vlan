// Copyright 2026 Authors of spidernet-io
// SPDX-License-Identifier: Apache-2.0

// Package networking implements the pre-flight connectivity validation for
// cloud-assigned IP/VLAN/MAC triples.
//
// The ARP send path follows the same AF_PACKET/SOCK_DGRAM pattern as
// spiderpool pkg/networking/networking/packet.go (SendARPReuqest): the kernel
// constructs the Ethernet header, so the source MAC is automatically the real
// MAC of the probing interface. This matters on IaaS fabrics with strict
// anti-spoofing: only packets with source IP = bound IP, source MAC = sub-ENI
// MAC and the correct VLAN are forwarded, so a reply from the gateway proves
// the whole cloud configuration is live.
package networking

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"
)

const (
	// ARPOperationRequest is the ARP request operation code.
	ARPOperationRequest = 1
	// ARPOperationReply is the ARP reply operation code.
	ARPOperationReply = 2

	// arpPacketSize is the size of an Ethernet IPv4 ARP payload.
	arpPacketSize = 28
)

// ARPPacket is a parsed Ethernet IPv4 ARP payload.
type ARPPacket struct {
	Operation uint16
	SenderMAC net.HardwareAddr
	SenderIP  net.IP
	TargetMAC net.HardwareAddr
	TargetIP  net.IP
}

// BuildARPRequest builds an Ethernet IPv4 ARP request payload (without
// Ethernet header, which is filled in by the kernel for SOCK_DGRAM sockets).
// senderIP must be the real cloud-allocated IPv4 address: probes sourced from
// 0.0.0.0 or unbound IPs are dropped by the IaaS fabric anti-spoofing.
func BuildARPRequest(senderMAC net.HardwareAddr, senderIP, targetIP net.IP) ([]byte, error) {
	sip := senderIP.To4()
	if sip == nil {
		return nil, fmt.Errorf("sender IP %q is not an IPv4 address", senderIP)
	}
	tip := targetIP.To4()
	if tip == nil {
		return nil, fmt.Errorf("target IP %q is not an IPv4 address", targetIP)
	}
	if len(senderMAC) != 6 {
		return nil, fmt.Errorf("invalid sender MAC %q", senderMAC)
	}

	b := new(bytes.Buffer)
	_ = binary.Write(b, binary.BigEndian, uint16(1))                // Hardware type: Ethernet
	_ = binary.Write(b, binary.BigEndian, uint16(syscall.ETH_P_IP)) // Protocol type: IPv4
	_ = binary.Write(b, binary.BigEndian, uint8(6))                 // Hardware address length
	_ = binary.Write(b, binary.BigEndian, uint8(4))                 // Protocol address length
	_ = binary.Write(b, binary.BigEndian, uint16(ARPOperationRequest))
	_, _ = b.Write(senderMAC)                                  // Sender hardware address
	_, _ = b.Write(sip)                                        // Sender protocol address
	_, _ = b.Write([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) // Target hardware address (broadcast)
	_, _ = b.Write(tip)                                        // Target protocol address
	return b.Bytes(), nil
}

// ParseARPPacket parses an Ethernet IPv4 ARP payload.
func ParseARPPacket(b []byte) (*ARPPacket, error) {
	if len(b) < arpPacketSize {
		return nil, fmt.Errorf("ARP packet too short: %d bytes", len(b))
	}
	if binary.BigEndian.Uint16(b[0:2]) != 1 || binary.BigEndian.Uint16(b[2:4]) != syscall.ETH_P_IP {
		return nil, errors.New("not an Ethernet IPv4 ARP packet")
	}
	if b[4] != 6 || b[5] != 4 {
		return nil, errors.New("unexpected ARP address lengths")
	}
	return &ARPPacket{
		Operation: binary.BigEndian.Uint16(b[6:8]),
		SenderMAC: net.HardwareAddr(append([]byte(nil), b[8:14]...)),
		SenderIP:  net.IP(append([]byte(nil), b[14:18]...)),
		TargetMAC: net.HardwareAddr(append([]byte(nil), b[18:24]...)),
		TargetIP:  net.IP(append([]byte(nil), b[24:28]...)),
	}, nil
}

// IsGatewayReply reports whether pkt is an ARP reply sent by gatewayIP.
func IsGatewayReply(pkt *ARPPacket, gatewayIP net.IP) bool {
	return pkt != nil && pkt.Operation == ARPOperationReply && pkt.SenderIP.Equal(gatewayIP.To4())
}

func htons(v uint16) uint16 {
	return (v << 8) | (v >> 8)
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
// TODO: IPv6 support via NS/NA probing.
func CheckGatewayReachable(ifName string, senderIP, gatewayIP net.IP, retries int, timeout time.Duration) error {
	ifi, err := net.InterfaceByName(ifName)
	if err != nil {
		return fmt.Errorf("failed to find interface %q: %w", ifName, err)
	}

	request, err := BuildARPRequest(ifi.HardwareAddr, senderIP, gatewayIP)
	if err != nil {
		return err
	}

	soc, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_DGRAM, int(htons(syscall.ETH_P_ARP)))
	if err != nil {
		return fmt.Errorf("failed to create AF_PACKET datagram socket: %w", err)
	}
	defer func() { _ = syscall.Close(soc) }()

	if err := syscall.Bind(soc, &syscall.SockaddrLinklayer{
		Protocol: htons(syscall.ETH_P_ARP),
		Ifindex:  ifi.Index,
	}); err != nil {
		return fmt.Errorf("failed to bind socket to %q: %w", ifName, err)
	}

	dst := &syscall.SockaddrLinklayer{
		Protocol: htons(syscall.ETH_P_ARP),
		Ifindex:  ifi.Index,
		Hatype:   1,
		Halen:    6,
		Addr:     [8]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}

	buf := make([]byte, 128)
	for i := 0; i < retries; i++ {
		if err := syscall.Sendto(soc, request, 0, dst); err != nil {
			return fmt.Errorf("failed to send ARP request on %q: %w", ifName, err)
		}

		deadline := time.Now().Add(timeout)
		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break // per-attempt timeout, retry
			}
			tv := syscall.NsecToTimeval(remaining.Nanoseconds())
			if err := syscall.SetsockoptTimeval(soc, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
				return fmt.Errorf("failed to set socket receive timeout: %w", err)
			}

			nb, _, err := syscall.Recvfrom(soc, buf, 0)
			if err != nil {
				if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EINTR) {
					break // timeout, retry
				}
				return fmt.Errorf("failed to receive ARP packet on %q: %w", ifName, err)
			}

			pkt, err := ParseARPPacket(buf[:nb])
			if err != nil {
				continue // unrelated packet
			}
			if IsGatewayReply(pkt, gatewayIP) {
				return nil
			}
		}
	}

	return fmt.Errorf("connectivity validation failed: no ARP reply from gateway %s on %q after %d attempts (%s each); the cloud-assigned IP/VLAN/MAC configuration is likely wrong or not yet effective",
		gatewayIP, ifName, retries, timeout)
}
