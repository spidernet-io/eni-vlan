// Copyright 2015 CNI authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Copyright 2026 Authors of spidernet-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"runtime"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	bv "github.com/containernetworking/plugins/pkg/utils/buildversion"
	"github.com/spidernet-io/eni-vlan/pkg/config"
	"github.com/spidernet-io/eni-vlan/pkg/vlan"
)

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

func cmdAdd(args *skel.CmdArgs) error {
	n, cniVersion, err := config.LoadConf(args)
	if err != nil {
		return err
	}

	var result *current.Result

	result, err = vlan.CmdAdd(args, n)
	if err != nil {
		return err
	}

	return types.PrintResult(result, cniVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	n, _, err := config.LoadConf(args)
	if err != nil {
		return err
	}

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %w", args.Netns, err)
	}
	defer func() {
		_ = netns.Close()
	}()

	// Always execute IPAM DEL
	if err := ipam.ExecDel(n.IPAM.Type, args.StdinData); err != nil {
		return err
	}

	// Delete VLAN interface if it exists
	err = vlan.DeleteVlan(args.IfName, netns)
	if err != nil {
		// Log error but don't fail if interface doesn't exist
		return fmt.Errorf("failed to delete VLAN interface: %w", err)
	}

	return nil
}

func cmdCheck(args *skel.CmdArgs) error {
	// TODO: implement CHECK command
	return nil
}

func cmdStatus(args *skel.CmdArgs) error {
	// TODO: implement STATUS command
	return nil
}

func main() {
	skel.PluginMainFuncs(skel.CNIFuncs{
		Add:    cmdAdd,
		Check:  cmdCheck,
		Del:    cmdDel,
		Status: cmdStatus,
	}, version.All, bv.BuildString("eni-vlan"))
}
