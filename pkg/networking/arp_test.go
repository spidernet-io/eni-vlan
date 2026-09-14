// Copyright 2026 Authors of spidernet-io
// SPDX-License-Identifier: Apache-2.0

package networking_test

import (
	"encoding/binary"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spidernet-io/eni-vlan/pkg/networking"
)

func mustMAC(s string) net.HardwareAddr {
	mac, err := net.ParseMAC(s)
	Expect(err).NotTo(HaveOccurred())
	return mac
}

// buildARPReply builds a raw Ethernet IPv4 ARP reply payload for tests.
func buildARPReply(senderMAC net.HardwareAddr, senderIP net.IP, targetMAC net.HardwareAddr, targetIP net.IP) []byte {
	b := make([]byte, 0, 28)
	b = binary.BigEndian.AppendUint16(b, 1)      // Hardware type: Ethernet
	b = binary.BigEndian.AppendUint16(b, 0x0800) // Protocol type: IPv4
	b = append(b, 6, 4)                          // Address lengths
	b = binary.BigEndian.AppendUint16(b, networking.ARPOperationReply)
	b = append(b, senderMAC...)
	b = append(b, senderIP.To4()...)
	b = append(b, targetMAC...)
	b = append(b, targetIP.To4()...)
	return b
}

var _ = Describe("ARP packet building", func() {
	senderMAC := mustMAC("fe:dc:ba:98:76:54")
	senderIP := net.ParseIP("12.175.227.100")
	gatewayIP := net.ParseIP("12.175.227.254")

	It("should build a valid ARP request", func() {
		pktBytes, err := networking.BuildARPRequest(senderMAC, senderIP, gatewayIP)
		Expect(err).NotTo(HaveOccurred())
		Expect(pktBytes).To(HaveLen(28))

		pkt, err := networking.ParseARPPacket(pktBytes)
		Expect(err).NotTo(HaveOccurred())
		Expect(pkt.Operation).To(Equal(uint16(networking.ARPOperationRequest)))
		Expect(pkt.SenderMAC.String()).To(Equal(senderMAC.String()))
		Expect(pkt.SenderIP.Equal(senderIP)).To(BeTrue())
		Expect(pkt.TargetMAC.String()).To(Equal("ff:ff:ff:ff:ff:ff"))
		Expect(pkt.TargetIP.Equal(gatewayIP)).To(BeTrue())
	})

	It("should reject a non-IPv4 sender IP", func() {
		_, err := networking.BuildARPRequest(senderMAC, net.ParseIP("fd00::1"), gatewayIP)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("IPv4"))
	})

	It("should reject a non-IPv4 target IP", func() {
		_, err := networking.BuildARPRequest(senderMAC, senderIP, net.ParseIP("fd00::1"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("IPv4"))
	})

	It("should reject an invalid sender MAC", func() {
		_, err := networking.BuildARPRequest(net.HardwareAddr{0x01}, senderIP, gatewayIP)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("MAC"))
	})
})

var _ = Describe("ARP packet parsing", func() {
	gwMAC := mustMAC("aa:bb:cc:dd:ee:ff")
	podMAC := mustMAC("fe:dc:ba:98:76:54")
	gwIP := net.ParseIP("12.175.227.254")
	podIP := net.ParseIP("12.175.227.100")

	It("should parse a gateway ARP reply", func() {
		raw := buildARPReply(gwMAC, gwIP, podMAC, podIP)
		pkt, err := networking.ParseARPPacket(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(pkt.Operation).To(Equal(uint16(networking.ARPOperationReply)))
		Expect(pkt.SenderMAC.String()).To(Equal(gwMAC.String()))
		Expect(pkt.SenderIP.Equal(gwIP)).To(BeTrue())
	})

	It("should reject a truncated packet", func() {
		_, err := networking.ParseARPPacket(make([]byte, 10))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("too short"))
	})

	It("should reject a non-ARP payload", func() {
		raw := make([]byte, 28)
		_, err := networking.ParseARPPacket(raw)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Gateway reply matching", func() {
	gwMAC := mustMAC("aa:bb:cc:dd:ee:ff")
	podMAC := mustMAC("fe:dc:ba:98:76:54")
	gwIP := net.ParseIP("12.175.227.254")
	podIP := net.ParseIP("12.175.227.100")

	It("should match an ARP reply from the gateway", func() {
		pkt, err := networking.ParseARPPacket(buildARPReply(gwMAC, gwIP, podMAC, podIP))
		Expect(err).NotTo(HaveOccurred())
		Expect(networking.IsGatewayReply(pkt, gwIP)).To(BeTrue())
	})

	It("should not match a reply from another host", func() {
		otherIP := net.ParseIP("12.175.227.1")
		pkt, err := networking.ParseARPPacket(buildARPReply(gwMAC, otherIP, podMAC, podIP))
		Expect(err).NotTo(HaveOccurred())
		Expect(networking.IsGatewayReply(pkt, gwIP)).To(BeFalse())
	})

	It("should not match an ARP request from the gateway", func() {
		request, err := networking.BuildARPRequest(gwMAC, gwIP, podIP)
		Expect(err).NotTo(HaveOccurred())
		pkt, err := networking.ParseARPPacket(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(networking.IsGatewayReply(pkt, gwIP)).To(BeFalse())
	})

	It("should not match nil packet", func() {
		Expect(networking.IsGatewayReply(nil, gwIP)).To(BeFalse())
	})
})
