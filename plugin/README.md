# lighthouse

## Name

*Lighthouse* - DNS Discovery for services across clusters.

## Description

*Lighthouse* is an external plugin for CoreDNS that allows Cross Cluster Service
Discovery between kubernetes clusters connected by [*Submariner*](https://github.com/submariner-io/submariner)

This is still in early proof-of-concept state.

## Syntax

Lighthouse requires [*kubernetes* plugin](https://github.com/coredns/coredns/blob/master/plugin/kubernetes/README.md)
to be present.

```
lighthouse
```

## External Plugin

*Lighthouse* is an *external* plugin, which means it is not included in CoreDNS releases.  To use *lighthouse*,
you'll need to build a CoreDNS image with *lighthouse*.

Steps to build lighthouse:

* Clone https://github.com/coredns/coredns into `$GOPATH/src/github.com/coredns`
* Add this plugin to [plugin.cfg](https://github.com/coredns/coredns/blob/master/plugin.cfg) per instructions therein.
```
kubernetes:kubernetes
lighthouse:github.com/submariner-io/lighthouse/plugin/lighthouse
```
* `make -f Makefile.release DOCKER=your-docker-repo release`
* `make -f Makefile.release DOCKER=your-docker-repo docker`
* `make -f Makefile.release DOCKER=your-docker-repo docker-push`

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
