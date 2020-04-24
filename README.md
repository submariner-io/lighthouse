# Lighthouse 
Lighthouse provides DNS discovery to Kubernetes clusters connected by [Submariner](https://github.com/submariner-io/submariner) in multi-cluster connected environments. The solution is compatible with any CNI (Container Network Interfaces) plugin.

# Architecture
The below digram shows the basic Lighthouse architecture.

![Lighthouse Architecture](https://raw.githubusercontent.com/submariner-io/lighthouse/master/docs/img/architecture.png)

## Lighthouse Agent
The Lighthouse Agent runs in every cluster and it has access to the Kubernetes api-server running in the broker. It creates a lighthouse crd for each service and sync the CRD with broker. It also retrieves the information about services running in another cluster from the broker and creates a lighthouse CRD locally.

The lighthouse agent will get updated, whenever a new lighthouse CRD is created or deleted in the broker.

### WorkFlow
The workflow is as follows,

- Lighthouse agent connects to the kube-api-server of the broker.
- It creates Lighthouse CRD for every service in the local cluster.
- It syncs the Lighthouse CRD to and from the broker.


![Lighthouse Controller WorkFlow](https://raw.githubusercontent.com/submariner-io/lighthouse/master/docs/img/controllerWorkFlow.png)

## Lighthouse Plugin
Lighthouse can be installed as an external plugin for CoreDNS, and will work along with the default Kubernetes plugin. It uses the Lighthouse CRD that is created by the lighthouse agent for DNS resolution. The below diagram indicates a high-level architecture.

![Lighthouse Plugin Architecture](https://raw.githubusercontent.com/submariner-io/lighthouse/master/docs/img/lighthousePluginArchitecture.png)

### WorkFlow
The workflow is as follows.

- A pod tries to resolve a Service name.
- The Kubernetes plugin in CoreDNS will first try to resolve the request. If no records are present the request will be sent to Lighthouse plugin.
- The Lighthouse plugin will use its MultiClusterService cache to try to resolve the request.
- If a record exists it will be returned, otherwise, the plugin will pass the request to the next plugin registered in CoreDNS.

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


