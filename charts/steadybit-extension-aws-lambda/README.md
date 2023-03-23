# Steadybit AWS Lambda Extension

This Helm chart adds the Steadybit scaffold extension to your Kubernetes cluster as a deployment.

## Quick Start

### Add Steadybit Helm repository

```
helm repo add steadybit-extension-aws-lambda https://steadybit.github.io/extension-aws-lambda
helm repo update
```

### Installing the Chart

To install the chart with the name `steadybit-extension-aws-lambda`.

```bash
$ helm upgrade steadybit-extension-aws-lambda \
    --install \
    --wait \
    --timeout 5m0s \
    --create-namespace \
    --namespace steadybit-extension \
    steadybit-extension-aws-lambda/steadybit-extension-aws-lambda
```
