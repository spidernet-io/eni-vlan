// Copyright 2026 Authors of spidernet-io
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"fmt"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
)

const (
	// DefaultValidationRetries is the default number of ARP probe attempts.
	DefaultValidationRetries = 3
	// DefaultValidationTimeoutMs is the default per-probe timeout in milliseconds.
	// Real IaaS gateway ARP RTT is measured at ~48ms, 500ms leaves ample margin.
	DefaultValidationTimeoutMs = 500
)

// NetConf represents the eni-vlan CNI network configuration.
// VLAN ID and MAC address are always resolved at runtime from
// spiderpool-agent after IPAM allocation; they cannot be set statically.
type NetConf struct {
	types.NetConf
	Master     string `json:"master"` // Master interface name (required)
	MTU        int    `json:"mtu,omitempty"`
	LinkContNs bool   `json:"linkInContainer,omitempty"`

	// ValidateIaasNetConfig controls the pre-flight connectivity validation:
	// after the VLAN sub-interface is created (real MAC set, link up, no IP yet),
	// an ARP probe (sender IP = allocated IP, target = gateway) verifies that the
	// cloud-assigned IP/VLAN/MAC triple actually works on the IaaS fabric.
	// Defaults to false.
	ValidateIaasNetConfig bool `json:"validateIaasNetConfig,omitempty"`
	// ValidationRetries is the number of ARP probe attempts before failing. Defaults to 3.
	ValidationRetries int `json:"validationRetries,omitempty"`
	// ValidationTimeoutMs is the per-probe reply timeout in milliseconds. Defaults to 500.
	ValidationTimeoutMs int `json:"validationTimeoutMs,omitempty"`
}

// LoadConf loads and validates the CNI configuration
func LoadConf(args *skel.CmdArgs) (*NetConf, string, error) {
	n := &NetConf{}
	if err := json.Unmarshal(args.StdinData, n); err != nil {
		return nil, "", fmt.Errorf("failed to load netconf: %w", err)
	}

	// Reject removed legacy fields loudly: static VLAN configuration is no
	// longer supported. Users needing static VLANs should use the community
	// vlan CNI plugin instead.
	var raw map[string]interface{}
	if err := json.Unmarshal(args.StdinData, &raw); err != nil {
		return nil, "", fmt.Errorf("failed to load netconf: %w", err)
	}
	for _, legacy := range []string{"vlanId", "vlanMode"} {
		if _, ok := raw[legacy]; ok {
			return nil, "", fmt.Errorf("field %q is no longer supported: eni-vlan always resolves VLAN/MAC dynamically from spiderpool-agent; for static VLAN configuration use the community vlan CNI plugin", legacy)
		}
	}

	if n.Master == "" {
		return nil, "", fmt.Errorf("\"master\" field is required")
	}

	if n.ValidationRetries == 0 {
		n.ValidationRetries = DefaultValidationRetries
	}
	if n.ValidationRetries < 0 {
		return nil, "", fmt.Errorf("invalid validationRetries %d (must be > 0)", n.ValidationRetries)
	}
	if n.ValidationTimeoutMs == 0 {
		n.ValidationTimeoutMs = DefaultValidationTimeoutMs
	}
	if n.ValidationTimeoutMs < 0 {
		return nil, "", fmt.Errorf("invalid validationTimeoutMs %d (must be > 0)", n.ValidationTimeoutMs)
	}

	return n, n.CNIVersion, nil
}

// IaasNetConfigValidationEnabled reports whether the pre-flight connectivity
// validation is enabled (default false).
func (n *NetConf) IaasNetConfigValidationEnabled() bool {
	return n.ValidateIaasNetConfig
}

// MarshalJSON implements custom JSON marshaling to handle embedded types.NetConf
func (n *NetConf) MarshalJSON() ([]byte, error) {
	// First marshal the embedded NetConf (which has custom MarshalJSON)
	netConfBytes, err := json.Marshal(&n.NetConf)
	if err != nil {
		return nil, err
	}

	// Unmarshal to map to combine with NetConf fields
	var combined map[string]interface{}
	if err := json.Unmarshal(netConfBytes, &combined); err != nil {
		return nil, err
	}

	// Add NetConf-specific fields
	if n.Master != "" {
		combined["master"] = n.Master
	}
	if n.MTU != 0 {
		combined["mtu"] = n.MTU
	}
	if n.LinkContNs {
		combined["linkInContainer"] = n.LinkContNs
	}
	if n.ValidateIaasNetConfig {
		combined["validateIaasNetConfig"] = n.ValidateIaasNetConfig
	}
	if n.ValidationRetries != 0 {
		combined["validationRetries"] = n.ValidationRetries
	}
	if n.ValidationTimeoutMs != 0 {
		combined["validationTimeoutMs"] = n.ValidationTimeoutMs
	}

	return json.Marshal(combined)
}

// UnmarshalJSON implements custom JSON unmarshaling to handle embedded types.NetConf
func (n *NetConf) UnmarshalJSON(data []byte) error {
	// First unmarshal to a map to extract NetConf fields
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Unmarshal the embedded NetConf
	netConfBytes, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(netConfBytes, &n.NetConf); err != nil {
		return err
	}

	// Extract NetConf-specific fields
	if v, ok := raw["master"]; ok {
		if s, ok := v.(string); ok {
			n.Master = s
		}
	}
	if v, ok := raw["mtu"]; ok {
		if mtuFloat, ok := v.(float64); ok {
			n.MTU = int(mtuFloat)
		}
	}
	if v, ok := raw["linkInContainer"]; ok {
		if b, ok := v.(bool); ok {
			n.LinkContNs = b
		}
	}
	if v, ok := raw["validateIaasNetConfig"]; ok {
		if b, ok := v.(bool); ok {
			n.ValidateIaasNetConfig = b
		}
	}
	if v, ok := raw["validationRetries"]; ok {
		if f, ok := v.(float64); ok {
			n.ValidationRetries = int(f)
		}
	}
	if v, ok := raw["validationTimeoutMs"]; ok {
		if f, ok := v.(float64); ok {
			n.ValidationTimeoutMs = int(f)
		}
	}

	return nil
}
