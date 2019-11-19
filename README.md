# Lighthouse 
Lighthouse provides DNS discovery to Kubernetes clusters connected by [Submariner](https://github.com/submariner-io/submariner) in multi-cluster connected environments. The solution is compatible with any CNI (Container Network Interfaces) plugin.

# Architecture
The below digram shows the basic Lighthouse architecture.

![Lighthouse Architecture](https://raw.githubusercontent.com/submariner-io/lighthouse/master/docs/img/architecture.png)

## Lighthouse Controller
This is the central discovery controller that gathers the information from the clusters, decides what information is to be shared and distributes the information as newly defined CRDs (Kubernetes custom resources).

The Lighthouse controller will be running along with the [Admiral](https://github.com/submariner-io/admiral) control plane. It uses the API’s exposed by the Admiral to connect to each cluster and listen for Service CRUD. Once it is notified that a new Service is created and needs to be distributed, it creates the Lighthouse CRD and the same is distributed to all the clusters that are part of the control plane. The flow for update and delete will be similar.

### WorkFlow
The typical workflow is as follows.

- Lighthouse controller registers with Admiral for cluster join and unjoin events.
- Once it is notified about a join event, it retrieves the credentials from Admiral and registers for a call back on Service creation with a shared informer.
- When a new Service is created in a cluster, the Lighthouse controller will be notified about it and it will create the Lighthouse CRD with the Service info.
- The controller with the help of Admiral distributes the CRD to all the clusters that have joined.

![Lighthouse Controller WorkFlow](https://raw.githubusercontent.com/submariner-io/lighthouse/master/docs/img/controllerWorkFlow.png)

## Lighthouse Plugin
Lighthouse can be installed as an external plugin for CoreDNS, and will work along with the default Kubernetes plugin. It uses the Lighthouse CRD that is distributed by the controller for DNS resolution. The below diagram indicates a high-level architecture.

![Lighthouse Plugin Architecture](https://raw.githubusercontent.com/submariner-io/lighthouse/master/docs/img/lighthousePluginArchitecture.png)

### WorkFlow
The typical workflow is as follows.

- A pod tries to resolve a Service name.
- The Kubernetes plugin in CoreDNS will try to resolve the request. If no records are present the request will be sent to Lighthouse plugin.
- Lighthouse CRD has built a cache, which was distributed by Lighthouse controller. The Lighthouse plugin will try to resolve the request according to its cache.
- If a record exists it will be returned, otherwise the plugin will pass the request to the next plugin registered in kube-dns.

![Lighthouse CoreDNS WorkFlow](https://raw.githubusercontent.com/submariner-io/lighthouse/master/docs/img/coreDNSWorkFlow.png)


# Setup development environment
You will need docker installed in your system, and at least 8GB of RAM.
Run:
```bash
   make e2e status=keep
```

# Testing

## To run end-to-end tests:
```bash
   make e2e
```

## To run unit tests:
```bash
   make test
```


