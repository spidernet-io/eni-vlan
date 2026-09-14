// Copyright 2026 Authors of spidernet-io
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"encoding/json"

	"github.com/containernetworking/cni/pkg/skel"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spidernet-io/eni-vlan/pkg/config"
)

var _ = Describe("Config Loading", func() {
	Context("valid configuration", func() {
		It("should load a minimal config with defaults", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			netConf, cniVersion, err := config.LoadConf(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(cniVersion).To(Equal("1.0.0"))
			Expect(netConf.Master).To(Equal("eth0"))
			Expect(netConf.ConnectivityCheckEnabled()).To(BeTrue())
			Expect(netConf.EnableConnectivityCheck).NotTo(BeNil())
			Expect(*netConf.EnableConnectivityCheck).To(BeTrue())
			Expect(netConf.CheckRetries).To(Equal(config.DefaultCheckRetries))
			Expect(netConf.CheckTimeoutMs).To(Equal(config.DefaultCheckTimeoutMs))
		})

		It("should honor explicit connectivity check fields", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"enableConnectivityCheck": false,
				"checkRetries": 5,
				"checkTimeoutMs": 1000,
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			netConf, _, err := config.LoadConf(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(netConf.ConnectivityCheckEnabled()).To(BeFalse())
			Expect(netConf.CheckRetries).To(Equal(5))
			Expect(netConf.CheckTimeoutMs).To(Equal(1000))
		})

		It("should keep enableConnectivityCheck true when set explicitly", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"enableConnectivityCheck": true,
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			netConf, _, err := config.LoadConf(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(netConf.ConnectivityCheckEnabled()).To(BeTrue())
		})

		It("should load MTU and linkInContainer", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"mtu": 1450,
				"linkInContainer": true,
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			netConf, _, err := config.LoadConf(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(netConf.MTU).To(Equal(1450))
			Expect(netConf.LinkContNs).To(BeTrue())
		})
	})

	Context("removed legacy fields", func() {
		It("should reject vlanId", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"vlanId": 100,
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			_, _, err := config.LoadConf(args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("vlanId"))
			Expect(err.Error()).To(ContainSubstring("no longer supported"))
		})

		It("should reject vlanMode", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"vlanMode": "auto",
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			_, _, err := config.LoadConf(args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("vlanMode"))
			Expect(err.Error()).To(ContainSubstring("no longer supported"))
		})
	})

	Context("common validation", func() {
		It("should reject config without master", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			_, _, err := config.LoadConf(args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("master"))
			Expect(err.Error()).To(ContainSubstring("required"))
		})

		It("should reject negative checkRetries", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"checkRetries": -1,
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			_, _, err := config.LoadConf(args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("checkRetries"))
		})

		It("should reject negative checkTimeoutMs", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"checkTimeoutMs": -100,
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			_, _, err := config.LoadConf(args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("checkTimeoutMs"))
		})

		It("should reject malformed JSON", func() {
			args := &skel.CmdArgs{
				StdinData: []byte(`{not-json`),
			}

			_, _, err := config.LoadConf(args)
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("NetConf JSON Serialization", func() {
	It("should marshal and unmarshal correctly", func() {
		enabled := false
		original := &config.NetConf{}
		original.Master = "eth0"
		original.MTU = 1500
		original.LinkContNs = true
		original.EnableConnectivityCheck = &enabled
		original.CheckRetries = 4
		original.CheckTimeoutMs = 800

		data, err := json.Marshal(original)
		Expect(err).NotTo(HaveOccurred())

		var parsed config.NetConf
		err = json.Unmarshal(data, &parsed)
		Expect(err).NotTo(HaveOccurred())

		Expect(parsed.Master).To(Equal("eth0"))
		Expect(parsed.MTU).To(Equal(1500))
		Expect(parsed.LinkContNs).To(BeTrue())
		Expect(parsed.EnableConnectivityCheck).NotTo(BeNil())
		Expect(*parsed.EnableConnectivityCheck).To(BeFalse())
		Expect(parsed.CheckRetries).To(Equal(4))
		Expect(parsed.CheckTimeoutMs).To(Equal(800))
	})

	It("should unmarshal to nil EnableConnectivityCheck when field absent", func() {
		data := []byte(`{"master": "eth0"}`)

		var parsed config.NetConf
		err := json.Unmarshal(data, &parsed)
		Expect(err).NotTo(HaveOccurred())

		Expect(parsed.EnableConnectivityCheck).To(BeNil())
		Expect(parsed.ConnectivityCheckEnabled()).To(BeTrue())
	})
})
