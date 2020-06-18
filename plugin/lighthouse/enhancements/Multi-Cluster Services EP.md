# Multi-Cluster Service Selection Optimization - EP

## Motivation

While the world is moving towards micro-services application based architecture, more organizations combine multi-cluster and multi-cloud providers for redundancy, performance, management, and other reasons.
We would like to optimize the total cost of a Kubernetes deployment in means of communication between multi-clusters, given that we could decide between different approaches. The three approaches possible, currently, are optimizing the full service chain of a single app/deployment, multiple apps/deployments, or a smart local optimization per cluster point of view, without building the full service chain.
In this Enhancement Proposal, we shall introduce questions, ideas, and requirements to optimize the service selection mechanism of the Lighthouse Plugin.


### Requirements
1. Topology:
    1. Selection Decision should be made locally, i.e per cluster - decentralized solution.
    2. The solution must be adaptive for topology changes.
1. Cost Parameters/Metrics:
    1. Latency
    2. RTT
    3. Networking cost
    4. Network congestion
    5. CPU/RAM load on each container/pod
    6. Service scale/autoscale (which actually will affect the load on the CPU and RAM)


### Open Questions
1. Topology:
    1. What topology would we like to optimize?
        1. Local optimization per cluster?
        2. Per App optimization?
        3. Multi App optimization?
    2. How often do deployments affects changes in the topology?
    3. How adaptive would we like to be for changes in the topology?
    4. Would we like to build the full application graph for better optimization?
        1.  How much are we willing to pay for the data sharing through the broker system?
1. Cost Parameters/Metrices:
    1.  How often would we like to collection the parameters?
    2.  Would we like to make the collection configurable?
1.  Optimization:
    1. Would we like to take the processing time of each service in account?


### Design Details
As we would like to comply with the Kubernetes KEP for the Multi-Cluster API, and the current implementation, we would like to offer a simple lightweight solution for selecting a service from a given pool.

#### Priority Queue

It seems that a data structure of a priority queue could be a simple elegant solution for selecting the best service per request.

#### External API

We would need to provide explicit API for updating the queue weights externally, as we would like to keep the implementation simple.
Maybe adding an additional property such as `cost` to the `status` of a `ServiceExport`
Then we would be able to update this cost property periodically.


### Related solutions and theoretical work

As service selection and placement has been researched in depth over the years, and especially in the last few years, we have collected a few academic articles about this subject showing different approaches for solving similar problems to what we face.

One of the first articles in the subject we encountered was a fairly new (end of 2019) thesis with cooperation of Ericsson AB [Latency-aware Optimization of the Existing Service Mesh in Edge Computing Environment](http://kth.diva-portal.org/smash/get/diva2:1334280/FULLTEXT01.pdf) . In the article, the writer  proposed a dynamic weighted (Latency-Aware, i.e. the weights of the round-robin are normalized according to the latency) Round Robin algorithm, implemented on top of the cutting edge service mesh Istio, which allows a centralized point of view over the whole service chain.
Networking metrics are collected periodically and optimization of the service chain is done (using black box optimization solutions), yielding new weights for each service instance, which in turn updates the weights for the weighted Round Robin mechanism of Istio.
In addition, with minor changes, we can introduce a general weight function to address cost as well as latency. The issue with this solution is that it is still using service mesh with all the problems we addressed in our goals.

Another new article (from Feb 2020) we encountered is the [QoS-Constrained Service Selection for Networked Microservices](https://ieeexplore.ieee.org/abstract/document/9000597) this paper presents a Microservice Service Selection algorithm (MSS) based on list scheduling. Firstly, a workflow model is employed to describe the service chain and to analyze the processing speed of instances, the transferring speed of the network and the degree of task concurrency, to calculate the sub-deadline of each task. Then, according to the sub-deadline and other information, the scheduling urgency of each task is calculated and updated in real-time. Finally, two service selection strategies based on the sub-deadline and urgency are proposed to complete the microservice instances selection and constitute the service path.


The authors of [Multi-objective scheduling of micro-services for optimal service function chains](https://ieeexplore.ieee.org/abstract/document/7996729) presents a similar problem to ours, where the goal is to reduce overall turn-around time for the complete end-to-end service chain and reduce the total traffic generated in the system.
They present the problem as a scheduling problem of microservices (as scheduling services in different location can improve traffic costs and improve performance) and offer a weighted affinity-based scheduling heuristic approach to solve the problem.

Another work in the subject, [Resource optimization of container orchestration a case study in multi-cloud microservices-based applications](https://link.springer.com/article/10.1007%2Fs11227-018-2345-2) extends the scheduling problem by adding a layer of multi-cloud distribution.
The author describes an approach to optimize the deployment of microservices-based applications in multi-cloud architectures in order to achieve the following objectives: cloud service cost, network latency among microservices, and time to start a new microservice when a provider becomes unavailable.

In [Load Balancing Across Microservices](https://ieeexplore.ieee.org/document/8486300), the authors suggests a service chain-oriented load balancing algorithm. The solution is based on message queues, trying to achieve load balancing according to micro-service requirements while minimizing the total chain turnaround time. Simulation were conducted, and the chain-oriented solution performs better than microservice-oriented and instance-oriented approaches. However, this approach has high time complexity which makes it obselete for industry solutions.
