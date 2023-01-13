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

package loadbalancer_test

import (
	cryptoRand "crypto/rand"
	"math/big"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/coredns/loadbalancer"
)

type server struct {
	name   string
	weight int64
}

var _ = Describe("Smooth Weighted RR", func() {
	// Global Vars
	var servers []server
	var smoothTestingServers []server
	var roundRobinServers []server
	var lb loadbalancer.Interface
	// Helpers
	randInt := func() int64 {
		maxWeight := int64(20)
		rInt, err := cryptoRand.Int(cryptoRand.Reader, big.NewInt(maxWeight))
		if err != nil {
			return int64(10)
		}
		return rInt.Int64()
	}

	addServer := func(s server) {
		err := lb.Add(s.name, s.weight)
		Expect(err).To(Succeed())
	}

	addAllServers := func(servers []server) {
		for _, s := range servers {
			addServer(s)
		}
	}

	getTotalServesWeight := func(servers []server) int64 {
		weight := int64(0)
		for _, v := range servers {
			weight += v.weight
		}
		return weight
	}

	// Validations
	validateServerAdded := func(s server) {
		for i := 0; i < 100; i++ {
			if lb.Next() == s.name {
				return
			}
		}
		Fail("Could not validate server presence")
	}

	validateAllServersAdded := func(servers []server) {
		for _, s := range servers {
			validateServerAdded(s)
		}
	}

	validateEmptyLBState := func() {
		Expect(lb.ItemCount()).To(Equal(0))
		for i := 0; i < 100; i++ {
			Expect(lb.Next()).To(BeNil())
		}
	}

	validateLoadBalancingByCount := func(rounds int, servers []server) {
		totalServersWeight := getTotalServesWeight(servers)
		results := make(map[string]int64)
		for i := 0; i < rounds; i++ {
			s := lb.Next().(string)
			results[s]++
		}
		for _, s := range servers {
			expectedWeight := int64(rounds) * s.weight / totalServersWeight
			// Expected weight should be +-1 from the weight counted
			Expect(results[s.name]).Should(Or(Equal(expectedWeight), Equal(expectedWeight-1), Equal(expectedWeight+1)))
		}
	}

	validateSmoothLoadBalancing := func(servers []server) {
		Expect(lb.Next().(string)).To(Equal(servers[0].name))
		Expect(lb.Next().(string)).To(Equal(servers[0].name))
		Expect(lb.Next().(string)).To(Equal(servers[1].name))
		Expect(lb.Next().(string)).To(Equal(servers[0].name))
		Expect(lb.Next().(string)).To(Equal(servers[2].name))
		Expect(lb.Next().(string)).To(Equal(servers[0].name))
		Expect(lb.Next().(string)).To(Equal(servers[0].name))
	}

	BeforeEach(func() {
		lb = loadbalancer.NewSmoothWeightedRR()
		smoothTestingServers = []server{
			{name: "server1", weight: 5},
			{name: "server2", weight: 1},
			{name: "server3", weight: 1},
		}
		rand.Seed(time.Now().UnixNano())
		servers = []server{
			{name: "server1", weight: randInt()},
			{name: "server2", weight: randInt()},
			{name: "server3", weight: randInt()},
		}

		roundRobinServers = []server{
			{name: "server1", weight: 1},
			{name: "server2", weight: 1},
			{name: "server3", weight: 1},
		}

		rand.Shuffle(len(servers), func(i, j int) { servers[i], servers[j] = servers[j], servers[i] })
	})

	When("first created", func() {
		It("should have an empty state", func() {
			validateEmptyLBState()
		})
	})

	When("all items are removed", func() {
		It("should have an empty state", func() {
			addAllServers(roundRobinServers)
			validateAllServersAdded(roundRobinServers)
			lb.RemoveAll()
			validateEmptyLBState()
		})
	})

	When("an item is added", func() {
		It("should be added to the internal state", func() {
			s := servers[0]
			addServer(s)
			validateServerAdded(s)
		})
	})

	When("a nil is added", func() {
		It("should return an error", func() {
			err := lb.Add(nil, 100)
			Expect(err).ToNot(BeNil())
			validateEmptyLBState()
		})
	})

	When("an item is added with a negative weight", func() {
		It("should return an error", func() {
			err := lb.Add(servers[0], -100)
			Expect(err).ToNot(BeNil())
			validateEmptyLBState()
		})
	})

	When("a single item is added", func() {
		It("Next() should return it all the time", func() {
			s := servers[0]
			addServer(s)
			for i := 0; i < 10; i++ {
				Expect(lb.Next()).To(Equal(s.name))
			}
		})
	})

	When("adding an item that is already present", func() {
		It("should return an error", func() {
			s := servers[0]
			addServer(s)
			err := lb.Add(s.name, s.weight)
			Expect(err).ToNot(BeNil())
			Expect(lb.ItemCount()).To(Equal(1))
		})
	})

	When("all items have equal weight", func() {
		It("should balance between them in an equal manner", func() {
			for _, s := range roundRobinServers {
				err := lb.Add(s.name, s.weight)
				Expect(err).To(BeNil())
			}
			for i := 0; i < 100; i++ {
				for _, s := range roundRobinServers {
					Expect(lb.Next().(string)).To(Equal(s.name))
				}
			}
		})
	})

	When("the items are weighted randomly", func() {
		It("should correctly balance between them", func() {
			addAllServers(servers)
			validateLoadBalancingByCount(100, servers)
		})
	})

	When("the items are weighted by 5,1,1", func() {
		It("should correctly balance between them", func() {
			addAllServers(smoothTestingServers)
			validateSmoothLoadBalancing(smoothTestingServers)
			validateLoadBalancingByCount(100, smoothTestingServers)
		})
	})

	When("an item is skipped", func() {
		It("should be omitted from the next round", func() {
			addAllServers(smoothTestingServers)
			// Normal Smoothing
			validateSmoothLoadBalancing(smoothTestingServers)
			// Skip the item
			failedItem := smoothTestingServers[0].name
			lb.Skip(failedItem)
			// Not to appear until a full round
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[1].name))
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[2].name))
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[0].name))
		})
	})

	When("a new item is added while balancing", func() {
		It("should accommodate the addition", func() {
			addAllServers(smoothTestingServers)

			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[0].name))
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[0].name))
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[1].name))

			newServer := server{name: "server4", weight: 1}
			smoothTestingServers = append(smoothTestingServers, newServer)
			err := lb.Add(newServer.name, newServer.weight)
			Expect(err).To(BeNil())

			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[0].name))
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[2].name))
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[0].name))
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[0].name))
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[3].name)) // new
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[0].name))
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[0].name))
			Expect(lb.Next().(string)).To(Equal(smoothTestingServers[1].name))

			validateLoadBalancingByCount(100, smoothTestingServers)
		})
	})
})
