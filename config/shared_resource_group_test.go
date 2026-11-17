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

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func sharedRGTestConfig() *Config {
	return &Config{
		Credentials: Credentials{
			SubscriptionID: "subscriptionID",
			ManagedIdentity: ManagedIdentityCredentials{
				ClientID: "clientID",
			},
		},
		Location:      "westeurope",
		ResourceGroup: "ci-vms-rg",
		VnetSubnetID:  "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-net/providers/Microsoft.Network/virtualNetworks/vnet/subnets/runners",
	}
}

func TestConfigValidateSharedResourceGroup(t *testing.T) {
	c := sharedRGTestConfig()
	c.DisableIsolatedNetworks = true
	assert.NoError(t, c.Validate())

	c = sharedRGTestConfig()
	c.DisableIsolatedNetworks = false
	assert.ErrorContains(t, c.Validate(), "resource_group requires disable_isolated_networks")

	// The subnet may come from the pool extra specs: the runner spec validation requires it.
	c = sharedRGTestConfig()
	c.DisableIsolatedNetworks = true
	c.VnetSubnetID = ""
	assert.NoError(t, c.Validate())
}
