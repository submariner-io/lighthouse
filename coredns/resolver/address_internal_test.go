/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resolver

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isPermittedEndpointAddress", func() {
	DescribeTable("should validate endpoint addresses",
		func(address string, expected bool) {
			Expect(isPermittedEndpointAddress(address)).To(Equal(expected))
		},
		Entry("valid IPv4 private address 10.x", "10.0.0.1", true),
		Entry("valid IPv4 private address 172.16.x", "172.16.5.4", true),
		Entry("valid IPv4 private address 192.168.x", "192.168.1.1", true),
		Entry("valid IPv4 public address", "203.0.113.7", true),
		Entry("valid IPv6 ULA address", "fd00::1", true),
		Entry("valid IPv6 documentation address", "2001:db8::1", true),
		Entry("empty string", "", false),
		Entry("invalid IP string", "not-an-ip", false),
		Entry("IPv4 unspecified address", "0.0.0.0", false),
		Entry("IPv6 unspecified address", "::", false),
		Entry("IPv4 loopback address", "127.0.0.1", false),
		Entry("IPv6 loopback address", "::1", false),
		Entry("IPv4 link-local address", "169.254.169.254", false),
		Entry("IPv6 link-local address", "fe80::1", false),
		Entry("IPv4 multicast address", "224.0.0.1", false),
		Entry("IPv6 multicast address", "ff02::1", false),
		Entry("IPv4 broadcast address", "255.255.255.255", false),
	)
})
