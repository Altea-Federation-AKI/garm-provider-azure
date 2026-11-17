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

package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/policy"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	azpolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	armcomputefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5/fake"
	armnetwork "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	armnetworkfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5/fake"
	"github.com/cloudbase/garm-provider-azure/config"
	"github.com/stretchr/testify/assert"
)

const sharedRG = "ci-vms-rg"

func sharedRGConfig() *config.Config {
	return &config.Config{
		Location:                "westeurope",
		ResourceGroup:           sharedRG,
		DisableIsolatedNetworks: true,
		VnetSubnetID:            "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-net/providers/Microsoft.Network/virtualNetworks/vnet/subnets/runners",
	}
}

func fakeOpts(transport azpolicy.Transporter) *policy.ClientOptions {
	return &policy.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}}
}

func TestResourceGroupFor(t *testing.T) {
	a := AzureCli{cfg: &config.Config{}}
	assert.Equal(t, "garm-abc", a.resourceGroupFor("garm-abc"))
	assert.Equal(t, "", a.SharedResourceGroup())

	a = AzureCli{cfg: sharedRGConfig()}
	assert.Equal(t, sharedRG, a.resourceGroupFor("garm-abc"))
	assert.Equal(t, sharedRG, a.SharedResourceGroup())
}

func TestCreateNetworkInterfaceSharedResourceGroupWithoutNSG(t *testing.T) {
	var gotRG string
	var gotNSG *armnetwork.SecurityGroup
	srv := armnetworkfake.InterfacesServer{}
	srv.BeginCreateOrUpdate = func(ctx context.Context, rg, name string, parameters armnetwork.Interface, options *armnetwork.InterfacesClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armnetwork.InterfacesClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
		gotRG = rg
		gotNSG = parameters.Properties.NetworkSecurityGroup
		resp.SetTerminalResponse(http.StatusOK, armnetwork.InterfacesClientCreateOrUpdateResponse{Interface: armnetwork.Interface{ID: to.Ptr("nic-id")}}, nil)
		return
	}
	nicCli, err := armnetwork.NewInterfacesClient("fake-id", &azfake.TokenCredential{}, fakeOpts(armnetworkfake.NewInterfacesServerTransport(&srv)))
	assert.NoError(t, err)

	a := AzureCli{cfg: sharedRGConfig(), nicCli: nicCli, location: "westeurope"}
	_, err = a.CreateNetWorkInterface(context.Background(), "garm-abc", "subnet-id", "", "", false)
	assert.NoError(t, err)
	assert.Equal(t, sharedRG, gotRG)
	assert.Nil(t, gotNSG)
}

func TestListVirtualMachinesSharedResourceGroup(t *testing.T) {
	poolID := "pool-1"
	other := "pool-2"
	var gotRG string
	srv := armcomputefake.VirtualMachinesServer{}
	srv.NewListPager = func(rg string, options *armcompute.VirtualMachinesClientListOptions) (resp azfake.PagerResponder[armcompute.VirtualMachinesClientListResponse]) {
		gotRG = rg
		resp.AddPage(http.StatusOK, armcompute.VirtualMachinesClientListResponse{
			VirtualMachineListResult: armcompute.VirtualMachineListResult{
				Value: []*armcompute.VirtualMachine{
					{Name: to.Ptr("garm-1"), Tags: map[string]*string{"garm-pool-id": &poolID}},
					{Name: to.Ptr("garm-2"), Tags: map[string]*string{"garm-pool-id": &other}},
					{Name: to.Ptr("not-garm")},
				},
			},
		}, nil)
		return
	}
	srv.NewListAllPager = func(options *armcompute.VirtualMachinesClientListAllOptions) (resp azfake.PagerResponder[armcompute.VirtualMachinesClientListAllResponse]) {
		t.Fatal("subscription-wide listing must not be used with a shared resource group")
		return
	}
	vmCli, err := armcompute.NewVirtualMachinesClient("fake-id", &azfake.TokenCredential{}, fakeOpts(armcomputefake.NewVirtualMachinesServerTransport(&srv)))
	assert.NoError(t, err)

	a := AzureCli{cfg: sharedRGConfig(), vmCli: vmCli}
	vms, err := a.ListVirtualMachines(context.Background(), poolID)
	assert.NoError(t, err)
	assert.Equal(t, sharedRG, gotRG)
	assert.Len(t, vms, 1)
	assert.Equal(t, "garm-1", *vms[0].Name)
}

func TestDeleteInstanceResources(t *testing.T) {
	for _, tc := range []struct {
		name     string
		notFound bool
	}{
		{name: "all resources exist"},
		{name: "resources already gone", notFound: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleted := map[string]string{}
			var forced bool

			vmSrv := armcomputefake.VirtualMachinesServer{}
			vmSrv.BeginDelete = func(ctx context.Context, rg, name string, options *armcompute.VirtualMachinesClientBeginDeleteOptions) (resp azfake.PollerResponder[armcompute.VirtualMachinesClientDeleteResponse], errResp azfake.ErrorResponder) {
				forced = options != nil && options.ForceDeletion != nil && *options.ForceDeletion
				if tc.notFound {
					errResp.SetResponseError(http.StatusNotFound, "NotFound")
					return
				}
				deleted["vm"] = rg + "/" + name
				resp.SetTerminalResponse(http.StatusOK, armcompute.VirtualMachinesClientDeleteResponse{}, nil)
				return
			}
			nicSrv := armnetworkfake.InterfacesServer{}
			nicSrv.BeginDelete = func(ctx context.Context, rg, name string, options *armnetwork.InterfacesClientBeginDeleteOptions) (resp azfake.PollerResponder[armnetwork.InterfacesClientDeleteResponse], errResp azfake.ErrorResponder) {
				if tc.notFound {
					errResp.SetResponseError(http.StatusNotFound, "NotFound")
					return
				}
				deleted["nic"] = rg + "/" + name
				resp.SetTerminalResponse(http.StatusOK, armnetwork.InterfacesClientDeleteResponse{}, nil)
				return
			}
			diskSrv := armcomputefake.DisksServer{}
			diskSrv.BeginDelete = func(ctx context.Context, rg, name string, options *armcompute.DisksClientBeginDeleteOptions) (resp azfake.PollerResponder[armcompute.DisksClientDeleteResponse], errResp azfake.ErrorResponder) {
				if tc.notFound {
					errResp.SetResponseError(http.StatusNotFound, "NotFound")
					return
				}
				deleted["disk"] = rg + "/" + name
				resp.SetTerminalResponse(http.StatusOK, armcompute.DisksClientDeleteResponse{}, nil)
				return
			}
			vmCli, err := armcompute.NewVirtualMachinesClient("fake-id", &azfake.TokenCredential{}, fakeOpts(armcomputefake.NewVirtualMachinesServerTransport(&vmSrv)))
			assert.NoError(t, err)
			nicCli, err := armnetwork.NewInterfacesClient("fake-id", &azfake.TokenCredential{}, fakeOpts(armnetworkfake.NewInterfacesServerTransport(&nicSrv)))
			assert.NoError(t, err)
			diskCli, err := armcompute.NewDisksClient("fake-id", &azfake.TokenCredential{}, fakeOpts(armcomputefake.NewDisksServerTransport(&diskSrv)))
			assert.NoError(t, err)

			a := AzureCli{cfg: sharedRGConfig(), vmCli: vmCli, nicCli: nicCli}
			a.SetDisksClient(diskCli)
			assert.NoError(t, a.DeleteInstanceResources(context.Background(), "garm-abc"))
			assert.True(t, forced)
			if tc.notFound {
				assert.Empty(t, deleted)
			} else {
				assert.Equal(t, map[string]string{"vm": sharedRG + "/garm-abc", "nic": sharedRG + "/garm-abc", "disk": sharedRG + "/garm-abc"}, deleted)
			}
		})
	}
}

func TestDeleteInstanceResourcesRequiresSharedResourceGroup(t *testing.T) {
	a := AzureCli{cfg: &config.Config{}}
	assert.ErrorContains(t, a.DeleteInstanceResources(context.Background(), "garm-abc"), "no shared resource group")
}
