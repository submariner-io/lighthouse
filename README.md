# Lighthouse 
Lighthouse provides DNS discovery to Kubernetes clusters connected by [Submariner](https://github.com/submariner-io/submariner) in multi-cluster connected environments. The solution is compatible with any CNI (Container Network Interfaces) plugin.

# Architecture
The lighthouse architecture is explained in detail at [Lighthouse Architecture](https://submariner-io.github.io/architecture/components/lighthouse/) 


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


