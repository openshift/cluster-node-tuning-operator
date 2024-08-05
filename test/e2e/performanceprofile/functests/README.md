The functional tests (functests) are split across suites as follows:

0_config: should be run before other in order to config the environment, thus, deploy PerformanceProfile:
    - Create PerformanceProfile
    - Wait for the MCP to pick PerformanceProfile MC
    - Wait for the MCP being updated
This deployment takes some time and requires reboot

1_performance: Performance performance test: Should run after config and before update ones
2_performance_update: Performance update: Update test, should run after performance ones
    - Apply different PerformanceProfiles and its updates to check if changes has been applied correctly
3_performance_status: Check the status of different objects related with PAO
4_latency: Runs tests of latency measurement tools and verifies the measurements are in the expected range according to the values of the latency environment variables
5_latency_testing: Runs the tests of suite 4_Latency with different values of environment variables to validate that the main tests are properly executed, skipped, or failed when needed.
6_mustgather_testing: Check if must-gather cluster generated data is correct 

Tests are executed in order of file-names
So be careful with renaming existing or adding new suites

Environment variables:
DISCOVERY_MODE: to get an already deployed performanceProfile.
If DISCOVERY_MODE set to true the suites will search for a PerformanceProfile on the cluster and use it.
If no PerformanceProfile is found, that suites will be skipped.

RESERVED_CPU_SET, ISOLATED_CPU_SET, OFFLINED_CPU_SET: strings that present the CPU sets distributed between reserved, isolated, and offlined CPU profile specifications. The runner is responsible for validating that these values are compatible with the testing environment.

### Running Tests on HyperShift Platform

When running e2e tests locally on a HyperShift platform, or in any environment that is different from the standard CI,
there are a couple of environment variables that should be set in advance:

* `CLUSTER_TYPE=hypershift`: A const that used to configure the e2e tests for running on HyperShift.

* `CLUSTER_NAME`: Represents the name of the hosted-cluster.
When running locally,
the name is the output of  `oc get hc <name-of-your-hosted-cluster> -n clusters --no-headers -o custom-columns=NAME:.metadata.name`

* `HYPERSHIFT_MANAGEMENT_CLUSTER_KUBECONFIG`: The path for the kubeconfig file of the management/hub cluster.

* `KUBECONFIG`: The path for the kubeconfig file of the hosted cluster.

* `HYPERSHIFT_HOSTED_CONTROL_PLANE_NAMESPACE`: The name of the namespace where all the control-plane components reside.
This namespace name usually is `clusters-$CLUSTER_NAME`  
