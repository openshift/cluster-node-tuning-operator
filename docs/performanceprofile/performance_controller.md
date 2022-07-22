# Performance Profile Controller

The `Performance Profile Controller` optimizes OpenShift clusters for applications sensitive to cpu and network latency.

![alt text](https://github.com/openshift/cluster-node-tuning-operator/blob/master/docs/performanceprofile/interactions/diagram.png "How Performance Profile Controller interacts with other components and operators")

## PerformanceProfile

The `PerformanceProfile` CRD is the API of the Performance Profile Controller and offers high level options
for applying various performance tunings to cluster nodes.

The performance profile API is documented in detail in the [Performance Profile](performance_profile.md) doc.
Follow the [API versions](api-versions.md) doc to check the supported API versions.

# Building and pushing the operator images

TBD

# Deploying

If you use your own images, make sure they are made public in your quay.io account!

Deploy a perfomance profile configuration by running:

```
CLUSTER=manual make cluster-deploy-pao
```

This will deploy

- a `MachineConfigPool` for the nodes which will be tuned
- a `PerformanceProfile`

The deployment will be retried in a loop until everything is deployed successfully, or until it times out.

> Note: `CLUSTER=manual` lets the deploy script use the `test/e2e/performanceprofile/cluster-setup/manual-cluster/performance/` kustomization directory.
In CI the `test/e2e/performanceprofile/cluster-setup/ci-cluster/performance/` dir will be used. The difference is that the CI cluster will deploy
the PerformanceProfile in the test code, while the `manual` cluster includes it in the kustomize based deployment.


Now you need to label the nodes which should be tuned. This can be done with

```
make cluster-label-worker-cnf
```

This will label 1 worker node with the `worker-cnf` role, and OCP's `Machine Config Operator` will start tuning this node.

In order to wait until MCO is ready, you can watch the `MachineConfigPool` until it is marked as updated with 

```
CLUSTER=manual make cluster-wait-for-pao-mcp
```

> Note: Be aware this can take quite a while (many minutes)

> Note: in CI this step is skipped, because the test code will wait for the MCP being up to date.

# Render mode

The operator can render manifests for all the components it supposes to create, based on Given a `PerformanceProfile`  

You need to provide the following environment variables
```
export PERFORMANCE_PROFILE_INPUT_FILES=<comma separated list of your Performance Profiles>
export ASSET_OUTPUT_DIR=<output path for the rendered manifests>
```

Build and invoke the binary
```
_output/cluster-node-tuning-operator render
```

Or provide the variables via command line arguments
```
_output/cluster-node-tuning-operator render --performance-profile-input-files <path> --asset-output-dir<path>
```

# Troubleshooting

When the deployment fails, or the performance tuning does not work as expected, follow the [Troubleshooting Guide](troubleshooting.md)
for debugging the cluster. Please provide as much info from troubleshooting as possible when reporting issues. Thanks!

# Testing

## Unit tests

Unit tests can be executed with `make test-unit`.

## Func tests

The functional tests are located in `/functests`. They can be executed with `make pao-functests-only` on a cluster with a
deployed Performance Profile Controller and configured MCP and nodes. It will create its own Performance profile!

### Latency test

The latency-test container image gives the possibility to run the latency 
test without need to install go, ginkgo or other go related modules.

The test itself is running the `oslat` `cyclictest` and `hwlatdetect` binaries and verifies if the maximal latency returned by each one of the tools is
less than specified value under the `MAXIMUM_LATENCY`.

To run the latency test inside the container:

```
docker run --rm -v /kubeconfig:/kubeconfig \
-e KUBECONFIG=/kubeconfig \
-e LATENCY_TEST_RUN=true \
-e LATENCY_TEST_RUNTIME=60 \
-e MAXIMUM_LATENCY=700 \
 quay.io/openshift-kni/cnf-tests /usr/bin/run-tests.sh
```

You can run the container with different ENV variables, but the bare minimum is to pass
`KUBECONFIG` mount and ENV variable, to give to the test access to the cluster and
`LATENCY_TEST_RUN=true` to run the latency test.

- `LATENCY_TEST_DELAY` indicates an (optional) delay in seconds to be used between the container is created and the tests actually start. Default is zero (start immediately).
- `LATENCY_TEST_RUN` indicates if the latency test should run.
- `LATENCY_TEST_RUNTIME` the amount of time in seconds that the latency test should run.
- `LATENCY_TEST_IMAGE` the image that used under the latency test.
- `LATECNY_TEST_CPUS` the amount of CPUs the pod which run the latency test should request
- `OSLAT_MAXIMUM_LATENCY` the expected maximum latency for all buckets in us in the oslat test.
- `CYCLICTEST_MAXIMUM_LATENCY` the expected maximum latency for the cyclictest test.
- `HWLATDETECT_MAXIMUM_LATENCY` the expected maximum latency for the hwlatdetect test.
- `MAXIMUM_LATENCY` a unified value for the expected maximum latency for all tests (In case both provided, the specific variables will have precedence over the unified one).

