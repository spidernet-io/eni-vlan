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
			Expect(netConf.IaasNetConfigValidationEnabled()).To(BeFalse())
			Expect(netConf.ValidateIaasNetConfig).To(BeFalse())
			Expect(netConf.ValidationRetries).To(Equal(config.DefaultValidationRetries))
			Expect(netConf.ValidationTimeoutMs).To(Equal(config.DefaultValidationTimeoutMs))
		})

		It("should honor explicit connectivity check fields", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"validateIaasNetConfig": false,
				"validationRetries": 5,
				"validationTimeoutMs": 1000,
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			netConf, _, err := config.LoadConf(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(netConf.IaasNetConfigValidationEnabled()).To(BeFalse())
			Expect(netConf.ValidationRetries).To(Equal(5))
			Expect(netConf.ValidationTimeoutMs).To(Equal(1000))
		})

		It("should keep validateIaasNetConfig true when set explicitly", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"validateIaasNetConfig": true,
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			netConf, _, err := config.LoadConf(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(netConf.IaasNetConfigValidationEnabled()).To(BeTrue())
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

		It("should reject negative validationRetries", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"validationRetries": -1,
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			_, _, err := config.LoadConf(args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("validationRetries"))
		})

		It("should reject negative validationTimeoutMs", func() {
			conf := `{
				"cniVersion": "1.0.0",
				"name": "eni-network",
				"type": "eni-vlan",
				"master": "eth0",
				"validationTimeoutMs": -100,
				"ipam": {
					"type": "spiderpool"
				}
			}`

			args := &skel.CmdArgs{
				StdinData: []byte(conf),
			}

			_, _, err := config.LoadConf(args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("validationTimeoutMs"))
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
		original := &config.NetConf{}
		original.Master = "eth0"
		original.MTU = 1500
		original.LinkContNs = true
		original.ValidateIaasNetConfig = true
		original.ValidationRetries = 4
		original.ValidationTimeoutMs = 800

		data, err := json.Marshal(original)
		Expect(err).NotTo(HaveOccurred())

		var parsed config.NetConf
		err = json.Unmarshal(data, &parsed)
		Expect(err).NotTo(HaveOccurred())

		Expect(parsed.Master).To(Equal("eth0"))
		Expect(parsed.MTU).To(Equal(1500))
		Expect(parsed.LinkContNs).To(BeTrue())
		Expect(parsed.ValidateIaasNetConfig).To(BeTrue())
		Expect(parsed.ValidationRetries).To(Equal(4))
		Expect(parsed.ValidationTimeoutMs).To(Equal(800))
	})

	It("should default ValidateIaasNetConfig to false when field absent", func() {
		data := []byte(`{"master": "eth0"}`)

		var parsed config.NetConf
		err := json.Unmarshal(data, &parsed)
		Expect(err).NotTo(HaveOccurred())

		Expect(parsed.ValidateIaasNetConfig).To(BeFalse())
		Expect(parsed.IaasNetConfigValidationEnabled()).To(BeFalse())
	})
})
