# Performance Profile Creator (PPC)

A tool to automate the process of creating Performance Profile using the user supplied profile parameters.

## Software Components

1. A CLI tool part of the Node Tuning Operator image

## Flow

1. PPC consumes a must-gather output.
1. PPC output is a bunch of YAML data (PAO profile + NTO tuned part).

## Things to note before running Performance Profile Creator

1. Performance Profile Creator is present as an entrypoint (in /usr/bin/performance-profile-creator) in the Node Tuning Operator image.
1. It is assumed that we have a must-gather directory available where we run the tool.
    1. Option 1: Run must-gather tool like below and use its output dir when you run PPC.

       ```bash
        oc adm must-gather --image=quay.io/openshift-kni/performance-addon-operator-must-gather:4.9-snapshot --dest-dir=<dir>
       ```

    1. Option 2: Use an existing must-gather tarball decompressed to a directory.

## Building Performance Profile Creator binary and image

Developers can build the Performance Profile Creator images from the source tree using make targets.

1. Setup Environment variables

    ```bash
    export REGISTRY_NAMESPACE=<your quay.io namespace>
    export IMAGE_TAG=<the image tag to use> #defaults to "latest"
    export IMAGE_BUILD_CMD=podman
    ```

1. To build from Performance Profile Creator source:

   ```bash
   make build-performance-profile-creator
   ```

1. To build the Performance addon Operator image from source:

   ```bash
   make local-image
   ```

Alternatively, you can pull the latest master upstream image.  In the following examples, TAG has the format major.minor. For example, the TAG for OpenShift 4.11 will be 4.11:

```bash
podman pull quay.io/openshift/origin-cluster-node-tuning-operator:4.11
```

## Running Performance Profile Creator

Depending on how the must-gather directory was set up the operator can now run the Performance Profile Creator tool with the required parameters.

PPC Tool help output:

```bash
$ podman run --entrypoint performance-profile-creator quay.io/openshift/origin-cluster-node-tuning-operator:4.11 -h
A tool that automates creation of Performance Profiles

Usage:
  performance-profile-creator [flags]

Flags:
      --disable-ht                        Disable Hyperthreading
  -h, --help                              help for performance-profile-creator
      --info string                       Show cluster information; requires --must-gather-dir-path, ignore the other arguments. [Valid values: log, json] (default "log")
      --mcp-name string                   MCP name corresponding to the target machines (required)
      --must-gather-dir-path string       Must gather directory path (default "must-gather")
      --power-consumption-mode string     The power consumption mode.  [Valid values: default, low-latency, ultra-low-latency] (default "default")
      --profile-name string               Name of the performance profile to be created (default "performance")
      --reserved-cpu-count int            Number of reserved CPUs (required)
      --rt-kernel                         Enable Real Time Kernel (required)
      --split-reserved-cpus-across-numa   Split the Reserved CPUs across NUMA nodes (default true)
      --topology-manager-policy string    Kubelet Topology Manager Policy of the performance profile to be created. [Valid values: single-numa-node, best-effort, restricted] (default "restricted")
      --user-level-networking             Run with User level Networking(DPDK) enabled
```

1. Option 1: Example of using must-gather output dir (obtained after running must gather manually) along with required arguments

   ```bash
   podman run --entrypoint performance-profile-creator -v /path/to/must-gather-output:/must-gather:z \
   quay.io/openshift/origin-cluster-node-tuning-operator:4.11 --must-gather-dir-path /must-gather \
   --reserved-cpu-count 20 --mcp-name worker-cnf --rt-kernel false > performance-profile.yaml
   ```

1. Option 2: Example of using an existing must-gather tarball which is decompressed to a directory along with required arguments

   ```bash
   podman run --entrypoint performance-profile-creator -v /path/to/decompressed-tarball:/must-gather:z \
   quay.io/openshift/origin-cluster-node-tuning-operator:4.11 --must-gather-dir-path /must-gather \
   --reserved-cpu-count 20 --mcp-name worker-cnf --rt-kernel false > performance-profile.yaml
    ```

## Running Performance Profile Creator using Wrapper script

1. Example of how the following wrapper script can be used to create a performance profle:

   ```bash
   ./hack/run-perf-profile-creator.sh -t must-gather.tar.gz -- --mcp-name=worker-cnf --reserved-cpu-count=20 \
   --rt-kernel=false --split-reserved-cpus-across-numa=true --topology-manager-policy=restricted \
   --power-consumption-mode=low-latency > performace-profile.yaml
   ```

## Reserved CPU allocation across NUMA nodes

By default (`--split-reserved-cpus-across-numa=true`) the reserved CPUs are distributed across the NUMA nodes instead of being concentrated on a single node. Pass `--split-reserved-cpus-across-numa=false` to concentrate them on a single NUMA node.

When the reserved CPUs are split across the NUMA nodes and hyperthreading is enabled, they are allocated as whole physical cores handed out round-robin across the NUMA nodes that have CPUs. This keeps the hyperthread siblings of a core together and does not require every NUMA node to hold the same number of reserved CPUs, so the per-node counts may differ by a core when the request does not divide evenly. NUMA nodes without CPUs (memory-only nodes) are skipped.

Because whole cores are reserved, `--reserved-cpu-count` must be a multiple of the number of logical processors per core when hyperthreading is enabled (for example, a multiple of two on a typical 2-way SMT system); otherwise the tool exits with an error.

## Discovery mode

To learn about the key details of the cluster you want to create a profile for, you may use the `discovery` (aka `info`) mode:

```bash
   ./hack/run-perf-profile-creator.sh -t must-gather.tar.gz -- --info=log

```

The `info` option requires a value which drives the output format. Please refer to the online help of the performance-profile-creator
tool to learn about the supported formats.
