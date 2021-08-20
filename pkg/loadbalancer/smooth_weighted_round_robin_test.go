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
package loadbalancer

import (
	cryptoRand "crypto/rand"
	"math"
	"math/big"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type server struct {
	name   string
	weight float64
}

var _ = Describe("Smooth Weighted RR", func() {
	// Global Vars
	var servers []server
	var smoothTestingServers []server
	var roundRobinServers []server
	var lb *smoothWeightedRR
	// Helpers
	randFloat := func() float64 {
		minWeight := 0.0
		maxWeight := 20.0
		rInt, err := cryptoRand.Int(cryptoRand.Reader, big.NewInt(math.MaxInt64))
		if err != nil {
			return 10.0
		}
		f := float64(rInt.Int64()) / (1 << 63)
		return minWeight + f*(maxWeight-minWeight)
	}

	addServer := func(index int, lb *smoothWeightedRR) server {
		server := servers[index]
		err := lb.Add(server.name, server.weight)
		if err != nil {
			Fail(err.Error())
		}
		return server
	}

	addAllServers := func(lb *smoothWeightedRR) {
		for i := 0; i < len(servers); i++ {
			_ = addServer(i, lb)
		}
	}

	getTotalServesWeight := func(servers []server) float64 {
		var weight float64
		for _, v := range servers {
			weight += v.weight
		}
		return weight
	}

	// Validations
	validateServerAdded := func(index int, lb *smoothWeightedRR) {
		server := servers[index]
		serversMap := lb.itemsMap
		Expect(serversMap[server.name].weight).To(Equal(server.weight))
	}

	validateAllServersAdded := func(lb *smoothWeightedRR) {
		for i := 0; i < len(servers); i++ {
			validateServerAdded(i, lb)
		}
	}

	validateEmptyLBState := func(lb *smoothWeightedRR) {
		Expect(lb.ItemsCount()).To(Equal((0)))
		for i := 0; i < 100; i++ {
			Expect(lb.Next()).To(BeNil())
		}
	}

	validateLoabBalancing := func(rounds int, servers []server, lb *smoothWeightedRR) {
		totalServersWeight := getTotalServesWeight(servers)

		results := make(map[string]float64)

		for i := 0; i < rounds; i++ {
			s := lb.Next().(string)
			results[s]++
		}

		for i := 0; i < len(servers); i++ {
			s := servers[i]
			expectedWeight := float64(rounds) * s.weight / totalServersWeight
			// Expected weight should be +-1 from the weight counted
			Expect(results[s.name]).Should(Or(Equal(math.Ceil(expectedWeight)), Equal(math.Floor(expectedWeight))))
		}
	}

	BeforeEach(func() {
		lb = NewSmoothWeightedRR().(*smoothWeightedRR)
		smoothTestingServers = []server{
			{name: "server1", weight: 5},
			{name: "server2", weight: 1},
			{name: "server3", weight: 1},
		}
		rand.Seed(time.Now().UnixNano())
		servers = []server{
			{name: "server1", weight: randFloat()},
			{name: "server2", weight: randFloat()},
			{name: "server3", weight: randFloat()},
		}

		roundRobinServers = []server{
			{name: "server1", weight: math.SmallestNonzeroFloat64},
			{name: "server2", weight: math.SmallestNonzeroFloat64},
			{name: "server3", weight: math.SmallestNonzeroFloat64},
		}

		rand.Shuffle(len(servers), func(i, j int) { servers[i], servers[j] = servers[j], servers[i] })
	})

	When("lb is created", func() {
		It("its state is new", func() {
			for _, l := range []*smoothWeightedRR{lb} {
				validateEmptyLBState(l)
			}
		})
	})

	When("removing all items", func() {
		It("should return an empty map", func() {
			for _, l := range []*smoothWeightedRR{lb} {
				addAllServers(l)
				validateAllServersAdded(l)
				l.RemoveAll()
				validateEmptyLBState(l)
			}
		})
	})

	When("a single item is added", func() {
		It("should fail on adding nil interface", func() {
			for _, l := range []*smoothWeightedRR{lb} {
				err := l.Add(nil, 100)
				Expect(err).ToNot(BeNil())
				validateEmptyLBState(l)
			}
		})

		It("should fail on adding negative weight", func() {
			for _, l := range []*smoothWeightedRR{lb} {
				err := l.Add(servers[0], -100)
				Expect(err).ToNot(BeNil())
				validateEmptyLBState(l)
			}
		})

		It("should return contain it", func() {
			for _, l := range []*smoothWeightedRR{lb} {
				_ = addServer(0, l)
				validateServerAdded(0, l)
			}
		})

		It("should return it all the time", func() {
			for _, l := range []*smoothWeightedRR{lb} {
				server := addServer(0, l)
				for i := 0; i < 10; i++ {
					Expect(l.Next()).To(Equal(server.name))
				}
			}
		})

		It("should not allow to add it again", func() {
			for _, l := range []*smoothWeightedRR{lb} {
				server := addServer(0, l)
				err := l.Add(server.name, server.weight)
				Expect(err).ToNot(BeNil())
				Expect(l.ItemsCount()).To(Equal(1))
			}
		})
	})

	When("load balancing with all weights equal 0 - i.e Round Robin", func() {
		It("should balance equaly", func() {
			for _, l := range []*smoothWeightedRR{lb} {
				var rrs = make([]server, len(roundRobinServers))
				_ = copy(rrs, roundRobinServers)
				for _, s := range rrs {
					err := l.Add(s.name, s.weight)
					Expect(err).To(BeNil())
				}

				for i := 0; i < 100; i++ {
					for _, s := range rrs {
						Expect(l.Next().(string)).To(Equal(s.name))
					}
				}
			}
		})
	})

	When("load balancing", func() {
		It("should balance between the servers by weight", func() {
			for _, l := range []*smoothWeightedRR{lb} {
				addAllServers(l)
				validateLoabBalancing(100, servers, l)
			}
		})

		It("should smooth balance", func() {
			for _, l := range []*smoothWeightedRR{lb} {
				var sts = make([]server, len(smoothTestingServers))
				_ = copy(sts, smoothTestingServers)
				for _, s := range sts {
					err := l.Add(s.name, s.weight)
					Expect(err).To(BeNil())
				}

				Expect(l.Next().(string)).To(Equal(sts[0].name))
				Expect(l.Next().(string)).To(Equal(sts[0].name))
				Expect(l.Next().(string)).To(Equal(sts[1].name))
				Expect(l.Next().(string)).To(Equal(sts[0].name))
				Expect(l.Next().(string)).To(Equal(sts[2].name))
				Expect(l.Next().(string)).To(Equal(sts[0].name))
				Expect(l.Next().(string)).To(Equal(sts[0].name))

				validateLoabBalancing(100, sts, l)
			}
		})

		When("a failure occur", func() {
			It("notifying on the failure should take affect", func() {
				for _, l := range []*smoothWeightedRR{lb} {
					var sts = make([]server, len(smoothTestingServers))
					_ = copy(sts, smoothTestingServers)
					for _, s := range sts {
						err := l.Add(s.name, s.weight)
						Expect(err).To(BeNil())
					}

					// Normal Smoothing
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[1].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[2].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					// After failure occur
					failedItem := sts[0].name
					l.ItemFailed(failedItem)
					// Not to appear until a full round
					Expect(l.Next().(string)).To(Equal(sts[1].name))
					Expect(l.Next().(string)).To(Equal(sts[2].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
				}
			})
		})

		When("adding new item while balancing", func() {
			It("should accommodate the addition", func() {
				for _, l := range []*smoothWeightedRR{lb} {
					var sts = make([]server, len(smoothTestingServers))
					_ = copy(sts, smoothTestingServers)
					for _, s := range sts {
						err := l.Add(s.name, s.weight)
						Expect(err).To(BeNil())
					}

					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[1].name))

					newServer := server{name: "server4", weight: 1}
					sts = append(sts, newServer)
					err := l.Add(newServer.name, newServer.weight)
					Expect(err).To(BeNil())

					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[2].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[3].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[1].name))

					validateLoabBalancing(100, sts, l)
				}
			})
		})
	})
})
