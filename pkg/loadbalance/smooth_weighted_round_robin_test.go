package loadbalance_test

import (
	cryptoRand "crypto/rand"
	"math"
	"math/big"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/lighthouse/pkg/loadbalance"
)

type server struct {
	name   string
	weight float64
}

var _ = Describe("Smooth Weighted RR Public API Test", func() {
	// Global Vars
	var servers []server
	var smoothTestingServers []server
	var roundRobinServers []server
	var lb *loadbalance.SmoothWeightedRR
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
	initState := func() {
		lb = loadbalance.NewSWRR()
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
	}

	addServer := func(index int, lb loadbalance.LoadBalancer) server {
		server := servers[index]
		err := lb.Add(server.name, server.weight)
		if err != nil {
			Fail(err.Error())
		}
		return server
	}

	addAllServers := func(lb loadbalance.LoadBalancer) {
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
	validateServerAdded := func(index int, lb loadbalance.LoadBalancer) {
		server := servers[index]
		serversMap := lb.All()
		Expect(serversMap[server.name]).To(Equal(server.weight))
	}

	validateAllServersAdded := func(lb loadbalance.LoadBalancer) {
		for i := 0; i < len(servers); i++ {
			validateServerAdded(i, lb)
		}
	}

	validateEmptyLBState := func(lb loadbalance.LoadBalancer) {
		Expect(len(lb.All())).To(Equal((0)))
		for i := 0; i < 100; i++ {
			Expect(lb.Next()).To(BeNil())
		}
	}

	validateLoabBalancing := func(rounds int, servers []server, lb loadbalance.LoadBalancer) {
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
		initState()
	})

	When("lb is created", func() {
		It("its state is new", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
				validateEmptyLBState(l)
			}
		})

		It("should not update, i.e fail", func() {
			server := servers[0]
			newWeight := 60.0
			for _, l := range []loadbalance.LoadBalancer{lb} {
				err := l.Update(server.name, newWeight)
				Expect(err).ToNot(BeNil())
				validateEmptyLBState(l)
			}
		})
	})

	When("removing all items", func() {
		It("should return an empty map", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
				addAllServers(l)
				validateAllServersAdded(l)
				l.RemoveAll()
				validateEmptyLBState(l)
			}
		})
	})

	When("a updating an item", func() {
		It("should fail on sending nil", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
				addAllServers(l)
				err := l.Update(nil, 100)
				Expect(err).ToNot(BeNil())
			}
		})

		It("should fail on adding negative weight", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
				addAllServers(l)
				err := l.Update(servers[0], -100)
				Expect(err).ToNot(BeNil())
			}
		})
	})

	When("a single item is added", func() {
		It("should fail on adding nil interface", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
				err := l.Add(nil, 100)
				Expect(err).ToNot(BeNil())
				validateEmptyLBState(l)
			}
		})

		It("should fail on adding negative weight", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
				err := l.Add(servers[0], -100)
				Expect(err).ToNot(BeNil())
				validateEmptyLBState(l)
			}
		})

		It("should return contain it", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
				_ = addServer(0, l)
				validateServerAdded(0, l)
			}
		})

		It("should return it all the time", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
				server := addServer(0, l)
				for i := 0; i < 10; i++ {
					Expect(l.Next()).To(Equal(server.name))
				}
			}
		})

		It("should not allow to add it again", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
				server := addServer(0, l)
				err := l.Add(server.name, server.weight)
				Expect(err).ToNot(BeNil())
				Expect(len(l.All())).To(Equal(1))
			}
		})

		It("should allow to update it", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
				server := addServer(0, l)
				validateServerAdded(0, l)

				newWeight := 60.0
				err := l.Update(server.name, newWeight)
				Expect(err).To(BeNil())

				serversMap := l.All()
				Expect(serversMap[server.name]).To(Equal(newWeight))
			}
		})
	})

	When("load balancing with all weights equal 0 - i.e Round Robin", func() {
		It("should balance equaly", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
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
			for _, l := range []loadbalance.LoadBalancer{lb} {
				addAllServers(l)
				validateLoabBalancing(100, servers, l)
			}
		})

		It("should smooth balance", func() {
			for _, l := range []loadbalance.LoadBalancer{lb} {
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

		When("reseting", func() {
			It("should reset the state", func() {
				for _, l := range []loadbalance.LoadBalancer{lb} {
					addAllServers(l)
					validateLoabBalancing(100, servers, l)
					l.Reset()
					validateLoabBalancing(500, servers, l)
				}
			})

			It("should reset smooth balance", func() {
				for _, l := range []loadbalance.LoadBalancer{lb} {
					var sts = make([]server, len(smoothTestingServers))
					_ = copy(sts, smoothTestingServers)
					for _, s := range sts {
						err := l.Add(s.name, s.weight)
						Expect(err).To(BeNil())
					}

					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[1].name))

					l.Reset()

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
		})

		When("updating weight while balancing", func() {
			It("should accommodate the update", func() {
				for _, l := range []loadbalance.LoadBalancer{lb} {
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

		When("adding item while balancing", func() {
			It("should accommodate the addition", func() {
				for _, l := range []loadbalance.LoadBalancer{lb} {
					var sts = make([]server, len(smoothTestingServers))
					_ = copy(sts, smoothTestingServers)
					for _, s := range sts {
						err := l.Add(s.name, s.weight)
						Expect(err).To(BeNil())
					}

					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[1].name))

					sts[1].weight = 3
					err := l.Update(sts[1].name, sts[1].weight)
					Expect(err).To(BeNil())

					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[2].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[1].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[1].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[2].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[1].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))
					Expect(l.Next().(string)).To(Equal(sts[0].name))

					validateLoabBalancing(100, sts, l)
				}
			})
		})
	})
})
