// Copyright 2026 Cloudbase Solutions SRL
//
//    Licensed under the Apache License, Version 2.0 (the "License"); you may
//    not use this file except in compliance with the License. You may obtain
//    a copy of the License at
//
//         http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
//    WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
//    License for the specific language governing permissions and limitations
//    under the License.

package spec

import (
	"encoding/json"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/cloudbase/garm-provider-common/params"
	"github.com/stretchr/testify/assert"
)

const sharedRGSubnetID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-net/providers/Microsoft.Network/virtualNetworks/vnet/subnets/runners"

func sharedRGRunnerSpec() *RunnerSpec {
	tools := params.RunnerApplicationDownload{
		OS:                to.Ptr("linux"),
		Architecture:      to.Ptr("x64"),
		DownloadURL:       to.Ptr("http://test.com"),
		Filename:          to.Ptr("runner.tar.gz"),
		SHA256Checksum:    to.Ptr("sha256:1123"),
		TempDownloadToken: to.Ptr("test-token"),
	}
	return &RunnerSpec{
		VMSize:             "Standard_D2ds_v6",
		AdminUsername:      "admin",
		StorageAccountType: "StandardSSD_LRS",
		DiskSizeGB:         64,
		BootstrapParams: params.BootstrapInstance{
			Name:          "garm-test",
			InstanceToken: "test-token",
			OSArch:        params.Amd64,
			OSType:        params.Linux,
			Image:         "Canonical:ubuntu-24_04-lts:server:latest",
			Flavor:        "Standard_D2ds_v6",
			Tools:         []params.RunnerApplicationDownload{tools},
			ExtraSpecs:    json.RawMessage(`{}`),
		},
		Tools:                   tools,
		Tags:                    map[string]*string{},
		VnetSubnetID:            sharedRGSubnetID,
		DisableIsolatedNetworks: true,
		ResourceGroup:           "ci-vms-rg",
	}
}

func TestRunnerSpecValidateSharedResourceGroup(t *testing.T) {
	assert.NoError(t, sharedRGRunnerSpec().Validate())

	s := sharedRGRunnerSpec()
	s.DisableIsolatedNetworks = false
	assert.ErrorContains(t, s.Validate(), "requires an existing subnet")

	s = sharedRGRunnerSpec()
	s.VnetSubnetID = ""
	assert.ErrorContains(t, s.Validate(), "requires an existing subnet")

	s = sharedRGRunnerSpec()
	s.AllocatePublicIP = true
	assert.ErrorContains(t, s.Validate(), "allocate_public_ip is not supported")

	s = sharedRGRunnerSpec()
	s.OpenInboundPorts = map[armnetwork.SecurityRuleProtocol][]int{armnetwork.SecurityRuleProtocolTCP: {22}}
	assert.ErrorContains(t, s.Validate(), "open_inbound_ports is not supported")
}

func TestGetNewVMPropertiesDeleteOptions(t *testing.T) {
	nicID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/ci-vms-rg/providers/Microsoft.Network/networkInterfaces/garm-test"

	props, err := sharedRGRunnerSpec().GetNewVMProperties(nicID, VMSizeEphemeralDiskSizeLimits{})
	assert.NoError(t, err)
	assert.Equal(t, armcompute.DiskDeleteOptionTypesDelete, *props.StorageProfile.OSDisk.DeleteOption)
	assert.Equal(t, armcompute.DeleteOptionsDelete, *props.NetworkProfile.NetworkInterfaces[0].Properties.DeleteOption)

	// Without a shared resource group nothing changes: the per-runner resource group is deleted instead.
	s := sharedRGRunnerSpec()
	s.ResourceGroup = ""
	props, err = s.GetNewVMProperties(nicID, VMSizeEphemeralDiskSizeLimits{})
	assert.NoError(t, err)
	assert.Nil(t, props.StorageProfile.OSDisk.DeleteOption)
	assert.Nil(t, props.NetworkProfile.NetworkInterfaces[0].Properties)
}
