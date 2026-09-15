// Copyright 2026 Authors of spidernet-io
// SPDX-License-Identifier: Apache-2.0

package networking_test

import (
	"net"

	"github.com/mdlayher/arp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spidernet-io/eni-vlan/pkg/networking"
)

func mustMAC(s string) net.HardwareAddr {
	mac, err := net.ParseMAC(s)
	Expect(err).NotTo(HaveOccurred())
	return mac
}

var _ = Describe("Gateway reply matching", func() {
	gwMAC := mustMAC("aa:bb:cc:dd:ee:ff")
	podMAC := mustMAC("fe:dc:ba:98:76:54")
	gwIP := net.ParseIP("12.175.227.254")
	podIP := net.ParseIP("12.175.227.100")

	It("should match an ARP reply from the gateway", func() {
		pkt, err := arp.NewPacket(arp.OperationReply, gwMAC, gwIP, podMAC, podIP)
		Expect(err).NotTo(HaveOccurred())
		Expect(networking.IsGatewayReply(pkt, gwIP)).To(BeTrue())
	})

	It("should not match a reply from another host", func() {
		otherIP := net.ParseIP("12.175.227.1")
		pkt, err := arp.NewPacket(arp.OperationReply, gwMAC, otherIP, podMAC, podIP)
		Expect(err).NotTo(HaveOccurred())
		Expect(networking.IsGatewayReply(pkt, gwIP)).To(BeFalse())
	})

	It("should not match an ARP request from the gateway", func() {
		pkt, err := arp.NewPacket(arp.OperationRequest, gwMAC, gwIP, podMAC, podIP)
		Expect(err).NotTo(HaveOccurred())
		Expect(networking.IsGatewayReply(pkt, gwIP)).To(BeFalse())
	})

	It("should not match nil packet", func() {
		Expect(networking.IsGatewayReply(nil, gwIP)).To(BeFalse())
	})
})
