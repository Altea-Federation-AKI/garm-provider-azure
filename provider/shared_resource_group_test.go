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

package provider

import (
	"context"
	"encoding/json"
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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	armresourcesfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/fake"
	"github.com/cloudbase/garm-provider-azure/config"
	azclient "github.com/cloudbase/garm-provider-azure/internal/client"
	"github.com/cloudbase/garm-provider-common/params"
	"github.com/stretchr/testify/assert"
)

const sharedRG = "ci-vms-rg"

func opts(transport azpolicy.Transporter) *policy.ClientOptions {
	return &policy.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}}
}

// sharedRGRecorder records the resource groups used by the fake Azure servers.
type sharedRGRecorder struct {
	nicRG, vmRG       string
	vmProps           *armcompute.VirtualMachineProperties
	nicNSG            *armnetwork.SecurityGroup
	deletedVM         string
	deletedNIC        string
	failVM            bool
	rgCreated         bool
	nsgCreated        bool
	vnetOrSubnetBuilt bool
}

func newSharedRGProvider(t *testing.T, rec *sharedRGRecorder) *azureProvider {
	t.Helper()
	cfg := &config.Config{
		Credentials: config.Credentials{
			SubscriptionID:  "subscriptionID",
			ManagedIdentity: config.ManagedIdentityCredentials{ClientID: "clientID"},
		},
		Location:                "westeurope",
		ResourceGroup:           sharedRG,
		DisableIsolatedNetworks: true,
		VnetSubnetID:            "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/ci-rg/providers/Microsoft.Network/virtualNetworks/ci-vnet/subnets/ci-vms",
	}

	rgSrv := armresourcesfake.ResourceGroupsServer{}
	rgSrv.CreateOrUpdate = func(ctx context.Context, name string, parameters armresources.ResourceGroup, options *armresources.ResourceGroupsClientCreateOrUpdateOptions) (resp azfake.Responder[armresources.ResourceGroupsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
		rec.rgCreated = true
		resp.SetResponse(http.StatusOK, armresources.ResourceGroupsClientCreateOrUpdateResponse{}, nil)
		return
	}
	nsgSrv := armnetworkfake.SecurityGroupsServer{}
	nsgSrv.BeginCreateOrUpdate = func(ctx context.Context, rg, name string, parameters armnetwork.SecurityGroup, options *armnetwork.SecurityGroupsClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armnetwork.SecurityGroupsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
		rec.nsgCreated = true
		resp.SetTerminalResponse(http.StatusOK, armnetwork.SecurityGroupsClientCreateOrUpdateResponse{SecurityGroup: armnetwork.SecurityGroup{ID: to.Ptr("nsg")}}, nil)
		return
	}
	vnetSrv := armnetworkfake.VirtualNetworksServer{}
	vnetSrv.BeginCreateOrUpdate = func(ctx context.Context, rg, name string, parameters armnetwork.VirtualNetwork, options *armnetwork.VirtualNetworksClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armnetwork.VirtualNetworksClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
		rec.vnetOrSubnetBuilt = true
		resp.SetTerminalResponse(http.StatusOK, armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, nil)
		return
	}
	nicSrv := armnetworkfake.InterfacesServer{}
	nicSrv.BeginCreateOrUpdate = func(ctx context.Context, rg, name string, parameters armnetwork.Interface, options *armnetwork.InterfacesClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armnetwork.InterfacesClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
		rec.nicRG = rg
		rec.nicNSG = parameters.Properties.NetworkSecurityGroup
		resp.SetTerminalResponse(http.StatusOK, armnetwork.InterfacesClientCreateOrUpdateResponse{Interface: armnetwork.Interface{ID: to.Ptr("/subscriptions/x/resourceGroups/" + rg + "/providers/Microsoft.Network/networkInterfaces/" + name)}}, nil)
		return
	}
	nicSrv.BeginDelete = func(ctx context.Context, rg, name string, options *armnetwork.InterfacesClientBeginDeleteOptions) (resp azfake.PollerResponder[armnetwork.InterfacesClientDeleteResponse], errResp azfake.ErrorResponder) {
		rec.deletedNIC = rg + "/" + name
		resp.SetTerminalResponse(http.StatusOK, armnetwork.InterfacesClientDeleteResponse{}, nil)
		return
	}
	vmSrv := armcomputefake.VirtualMachinesServer{}
	vmSrv.BeginCreateOrUpdate = func(ctx context.Context, rg, name string, parameters armcompute.VirtualMachine, options *armcompute.VirtualMachinesClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcompute.VirtualMachinesClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
		rec.vmRG = rg
		rec.vmProps = parameters.Properties
		if rec.failVM {
			errResp.SetResponseError(http.StatusConflict, "QuotaExceeded")
			return
		}
		resp.SetTerminalResponse(http.StatusOK, armcompute.VirtualMachinesClientCreateOrUpdateResponse{}, nil)
		return
	}
	vmSrv.BeginDelete = func(ctx context.Context, rg, name string, options *armcompute.VirtualMachinesClientBeginDeleteOptions) (resp azfake.PollerResponder[armcompute.VirtualMachinesClientDeleteResponse], errResp azfake.ErrorResponder) {
		rec.deletedVM = rg + "/" + name
		resp.SetTerminalResponse(http.StatusOK, armcompute.VirtualMachinesClientDeleteResponse{}, nil)
		return
	}

	rgCli, err := armresources.NewResourceGroupsClient("fake-id", &azfake.TokenCredential{}, opts(armresourcesfake.NewResourceGroupsServerTransport(&rgSrv)))
	assert.NoError(t, err)
	nsgCli, err := armnetwork.NewSecurityGroupsClient("fake-id", &azfake.TokenCredential{}, opts(armnetworkfake.NewSecurityGroupsServerTransport(&nsgSrv)))
	assert.NoError(t, err)
	vnetCli, err := armnetwork.NewVirtualNetworksClient("fake-id", &azfake.TokenCredential{}, opts(armnetworkfake.NewVirtualNetworksServerTransport(&vnetSrv)))
	assert.NoError(t, err)
	nicCli, err := armnetwork.NewInterfacesClient("fake-id", &azfake.TokenCredential{}, opts(armnetworkfake.NewInterfacesServerTransport(&nicSrv)))
	assert.NoError(t, err)
	vmCli, err := armcompute.NewVirtualMachinesClient("fake-id", &azfake.TokenCredential{}, opts(armcomputefake.NewVirtualMachinesServerTransport(&vmSrv)))
	assert.NoError(t, err)

	azCli := azclient.AzureCli{}
	azCli.SetConfig(cfg)
	azCli.SetLocation("westeurope")
	azCli.SetClients(rgCli, vnetCli, nil, nsgCli, nicCli, vmCli, nil, nil, nil)
	return &azureProvider{controllerID: "controllerID", azCli: &azCli, cfg: cfg}
}

func sharedRGBootstrapParams() params.BootstrapInstance {
	return params.BootstrapInstance{
		Name:          "garm-abc",
		InstanceToken: "test-token",
		OSArch:        params.Amd64,
		OSType:        params.Linux,
		Image:         "Canonical:ubuntu-24_04-lts:server:latest",
		Flavor:        "Standard_D2ds_v6",
		Tools: []params.RunnerApplicationDownload{{
			OS:                to.Ptr("linux"),
			Architecture:      to.Ptr("x64"),
			DownloadURL:       to.Ptr("http://test.com"),
			Filename:          to.Ptr("runner.tar.gz"),
			SHA256Checksum:    to.Ptr("sha256:1123"),
			TempDownloadToken: to.Ptr("test-token"),
		}},
		ExtraSpecs: json.RawMessage(`{}`),
	}
}

func TestCreateInstanceSharedResourceGroup(t *testing.T) {
	rec := &sharedRGRecorder{}
	a := newSharedRGProvider(t, rec)

	inst, err := a.CreateInstance(context.Background(), sharedRGBootstrapParams())
	assert.NoError(t, err)
	assert.Equal(t, "garm-abc", inst.ProviderID)

	assert.False(t, rec.rgCreated, "no per-runner resource group")
	assert.False(t, rec.nsgCreated, "no per-runner NSG")
	assert.False(t, rec.vnetOrSubnetBuilt, "existing subnet only")
	assert.Equal(t, sharedRG, rec.nicRG)
	assert.Nil(t, rec.nicNSG)
	assert.Equal(t, sharedRG, rec.vmRG)
	assert.Equal(t, armcompute.DiskDeleteOptionTypesDelete, *rec.vmProps.StorageProfile.OSDisk.DeleteOption)
	assert.Equal(t, armcompute.DeleteOptionsDelete, *rec.vmProps.NetworkProfile.NetworkInterfaces[0].Properties.DeleteOption)
}

func TestCreateInstanceSharedResourceGroupCleansUpOnFailure(t *testing.T) {
	rec := &sharedRGRecorder{failVM: true}
	a := newSharedRGProvider(t, rec)

	_, err := a.CreateInstance(context.Background(), sharedRGBootstrapParams())
	assert.ErrorContains(t, err, "failed to create VM")
	assert.Equal(t, sharedRG+"/garm-abc", rec.deletedVM)
	assert.Equal(t, sharedRG+"/garm-abc", rec.deletedNIC)
}

func TestDeleteInstanceSharedResourceGroup(t *testing.T) {
	rec := &sharedRGRecorder{}
	a := newSharedRGProvider(t, rec)

	assert.NoError(t, a.DeleteInstance(context.Background(), "garm-abc"))
	assert.Equal(t, sharedRG+"/garm-abc", rec.deletedVM)
	assert.Equal(t, sharedRG+"/garm-abc", rec.deletedNIC)
}
