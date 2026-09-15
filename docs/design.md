# eni-vlan Design Specification

> IaaS sub-ENI CNI plugin for the Spiderpool cloud network provider.

## Overview

eni-vlan is a CNI plugin dedicated to the cloud IaaS **sub-ENI + VLAN** scenario. The cloud platform allocates an IP address, a VLAN ID and a MAC address for each Pod NIC (a sub-ENI); spiderpool orchestrates the allocation and spiderpool-agent exposes the result on the node. eni-vlan consumes that result to build the Pod's VLAN sub-interface, and validates that the cloud-assigned configuration is actually live before the Pod starts.

Static VLAN configuration is intentionally **not** supported. Users who need a statically configured `vlanId` should use the [community vlan CNI plugin](https://github.com/containernetworking/plugins/tree/main/plugins/main/vlan).

## Execution Flow

### cmdAdd

```
┌─────────────────────────────────────────────────────────────────┐
│ 1. LoadConf: parse and validate CNI config                      │
│    (reject legacy vlanId/vlanMode fields)                       │
├─────────────────────────────────────────────────────────────────┤
│ 2. Parse K8S_POD_NAME / K8S_POD_NAMESPACE from CNI_ARGS         │
├─────────────────────────────────────────────────────────────────┤
│ 3. ipam.ExecAdd (spiderpool) → allocated IP(s) + gateway        │
├─────────────────────────────────────────────────────────────────┤
│ 4. GetWorkloadEndpoint via spiderpool-agent Unix socket         │
│    → VLAN ID + MAC for CNI_IFNAME                               │
├─────────────────────────────────────────────────────────────────┤
│ 5. CreateVlan: VLAN sub-interface on master, real sub-ENI MAC,  │
│    move into Pod netns, rename to CNI_IFNAME                    │
├─────────────────────────────────────────────────────────────────┤
│ 6. Connectivity validation (in Pod netns):                      │
│    link up (NO IP configured) → ARP probe to gateway            │
│    sender IP = allocated IP, sender MAC = interface real MAC    │
│      reply     → cloud config is live, continue                 │
│      timeout×N → FAIL CLOSED: ipam.ExecDel + delete interface   │
├─────────────────────────────────────────────────────────────────┤
│ 7. ConfigureIface: configure IP + routes from IPAM result       │
├─────────────────────────────────────────────────────────────────┤
│ 8. Return CNI result                                            │
└─────────────────────────────────────────────────────────────────┘
```

Every failure after step 3 rolls back the IPAM allocation (`ipam.ExecDel`); failures after step 5 also delete the created VLAN sub-interface.

### cmdDel

```
1. ipam.ExecDel (always)
2. Delete the VLAN sub-interface in the Pod netns (ignore if absent)
```

## Configuration

### NetConf Structure

```go
type NetConf struct {
    types.NetConf
    Master     string `json:"master"`          // required
    MTU        int    `json:"mtu,omitempty"`
    LinkContNs bool   `json:"linkInContainer,omitempty"`

    ValidateIaasNetConfig bool `json:"validateIaasNetConfig,omitempty"` // default false
    ValidationRetries            int   `json:"validationRetries,omitempty"`            // default 3
    ValidationTimeoutMs          int   `json:"validationTimeoutMs,omitempty"`          // default 500
}
```

Validation rules:

- `master` is required.
- `vlanId` and `vlanMode` are rejected with a descriptive error (removed legacy fields; static VLAN users are pointed to the community vlan CNI).
- `validationRetries` / `validationTimeoutMs` must be positive; zero means "use default".
- These fields are intended to be rendered and delivered by SpiderMultusConfig in the future, hence all connectivity-check fields have safe defaults.

### Example

```json
{
  "cniVersion": "1.0.0",
  "name": "eni-network",
  "type": "eni-vlan",
  "master": "eth0",
  "validateIaasNetConfig": true,
  "validationRetries": 3,
  "validationTimeoutMs": 500,
  "ipam": {
    "type": "spiderpool"
  }
}
```

## Spiderpool-Agent API: GetWorkloadEndpoint

eni-vlan queries spiderpool-agent via its Unix socket, following the same client pattern as the spiderpool IPAM plugin (`cmd/spiderpool`).

### Connection

- **Socket**: `/var/run/spidernet/spiderpool.sock` (well-known path, same as spiderpool IPAM)
- **Client**: spiderpool's `openapi.NewAgentOpenAPIUnixClient`

### RPC: GetWorkloadEndpoint

**Parameters**:

| Parameter | Required | Source | Description |
|-----------|----------|--------|-------------|
| `podName` | Yes | `CNI_ARGS` (`K8S_POD_NAME`) | Pod name |
| `podNamespace` | Yes | `CNI_ARGS` (`K8S_POD_NAMESPACE`) | Pod namespace |
| `nic` | No | `CNI_IFNAME` | If specified, return only this NIC's data |

### Response

Returns `WorkloadEndpointStatus` with an interfaces array:

```json
{
  "podName": "my-pod",
  "podNamespace": "default",
  "podUID": "...",
  "node": "node-1",
  "interfaces": [
    {
      "interface": "net1",
      "ipv4": "192.168.1.100/24",
      "ipv4Gateway": "192.168.1.1",
      "ipv6": "fd00::100/64",
      "vlan": 100,
      "mac": "aa:bb:cc:dd:ee:ff"
    }
  ]
}
```

The client finds the matching interface by `interface` name; a missing entry for `CNI_IFNAME` is a hard error.

## Connectivity Validation

### What it is

A **final pre-flight validation of the cloud-assigned IP/VLAN/MAC triple** — not a generic gateway health check and not RFC 5227 IP-conflict detection. It answers one question: *will an interface built with exactly this configuration be able to communicate through the IaaS fabric?*

### Why only eni-vlan can do it

Conclusions from real IaaS fabric testing (VLAN 3321, gateway 12.175.227.254):

| Probe packet | Result |
|---|---|
| real IP + real MAC → gateway | ✅ reply, RTT ~48ms |
| `0.0.0.0` → gateway (positive control) | ❌ no reply (dropped by fabric) |
| unbound IP + real MAC → gateway | ❌ no reply (fabric validates IP-MAC binding) |
| forged MAC → gateway | ❌ no reply (fabric validates MAC) |

- The fabric enforces strict anti-spoofing: only **source IP = bound IP + source MAC = real sub-ENI MAC + correct VLAN** packets are forwarded.
- RFC 5227-style probing (`0.0.0.0` sender) is therefore physically impossible; and since the fabric strictly binds IP to MAC, IP conflicts cannot occur — no conflict detection is needed.
- At the IPAM plugin stage the interface does not exist yet (no probe carrier). At the coordinator stage the IP is already configured on the interface, so the kernel passively answers ARP and pollutes fabric neighbor tables (lessons from spiderpool [#4582](https://github.com/spidernet-io/spiderpool/issues/4582)/[#4588](https://github.com/spidernet-io/spiderpool/issues/4588)).
- The **only safe window**: after eni-vlan creates the VLAN sub-interface with the real MAC and brings the link up, and strictly before any IP is configured.

### Probe mechanics

Implemented in `pkg/networking/arp.go`, modeled after spiderpool `pkg/networking/networking/packet.go` (`SendARPReuqest`) and the `Detector` receive loop in `ipam_detection.go`:

- `AF_PACKET` / `SOCK_DGRAM` socket bound to the VLAN sub-interface: the kernel builds the Ethernet header, so the source MAC is automatically the interface's real MAC.
- ARP request payload: sender IP = allocated IP, sender MAC = interface MAC, target = gateway IP, target MAC = broadcast.
- Receive loop with `SO_RCVTIMEO`; a packet matching `Operation == reply && SenderIP == gateway` proves the configuration is live.
- `validationRetries` attempts × `validationTimeoutMs` per attempt; executed inside the Pod netns via `ns.Do`.
- On timeout the CNI ADD **fails closed**: `ipam.ExecDel` + interface deletion, so the scheduler can retry with a fresh allocation.
- IPv4 ARP only; IPv6 NS/NA probing is a TODO. When the IPAM result has no IPv4 gateway, the check is skipped.

### Safety invariant

The probe MUST run while the interface has **no IP address configured**. A configured IP would make the kernel passively respond to ARP requests, polluting neighbor tables across the fabric.

## Error Handling

| Failure | Behavior |
|---|---|
| Config contains `vlanId`/`vlanMode` | CNI ADD fails with a pointer to the community vlan CNI |
| Missing `master` | CNI ADD fails |
| Missing `K8S_POD_NAME`/`K8S_POD_NAMESPACE` | CNI ADD fails |
| IPAM failure | CNI ADD fails (nothing to roll back) |
| Agent unreachable / no NIC entry | CNI ADD fails, IPAM rolled back |
| VLAN creation failure | CNI ADD fails, IPAM rolled back |
| Connectivity validation timeout | CNI ADD fails closed, IPAM rolled back, interface deleted |
| IP configuration failure | CNI ADD fails, IPAM rolled back, interface deleted |
| cmdDel with missing interface | Success (idempotent) |

## Implementation File Structure

```
cmd/eni-vlan/main.go        # skel entry: cmdAdd / cmdDel / cmdCheck / cmdStatus
pkg/config/config.go        # NetConf, LoadConf, legacy-field rejection, defaults
pkg/networking/arp.go       # ARP build/parse/probe (connectivity validation)
pkg/vlan/interface.go       # CreateVlan / DeleteVlan / UpdateMac / GetMTU
pkg/vlan/service.go         # CNI ADD flow orchestration
```

## Test Scenarios

- Config parsing: defaults for `validateIaasNetConfig`/`validationRetries`/`validationTimeoutMs`, explicit overrides, rejection of `vlanId`/`vlanMode`, missing `master`, invalid values, JSON round-trip.
- ARP logic (pure functions, no sockets): request building, payload parsing, gateway-reply matching, negative cases (truncated/non-ARP packets, replies from other hosts, requests instead of replies).
