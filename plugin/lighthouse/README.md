# lighthouse

## Name

*Lighthouse* - DNS Discovery for services across clusters.

## Description

*Lighthouse*  plugin allows Cross Cluster Service Discovery between Kubernetes 
clusters connected by [*Submariner*](https://github.com/submariner-io/submariner).

If the default Kubernetes plugin fails to resolve a DNS request, the lighthouse plugin will try to resolve it
using the information it gathered from other clusters that have joined the submariner control plane. On a successful resolution,
lighthouse plugin returns the cluster IP of the service in the remote cluster. Submariner ensures that this IP
is reachable.


## Syntax

Lighthouse requires [*kubernetes* plugin](https://github.com/coredns/coredns/blob/master/plugin/kubernetes/README.md)
to be present.

```
lighthouse
```
## Examples

```
. {
    errors
    log
    kubernetes cluster.local {
      fallthrough
    }
    lighthouse
}
```
