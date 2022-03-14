# CI

## OpenshiftCI

[OpenshiftCI](https://github.com/openshift/release) is used for running unit tests, building container images and
running e2e tests. The building blocks for the complete Ci configuration are:

- several Dockerfile located in the [openshift-ci](https://github.com/openshift-kni/performance-addon-operators/tree/master/openshift-ci) directory:
    - Dockerfile.tools: this image is used for building the binaries
    - Dockerfile.deploy: this image is used for building the operator image
    - Dockerfile.registry.*: these images are used for building the registry image:
        - intermediate: this is used as base image
        - build: this is used during CI run
        - upstream.dev: this is used for being pushed to quay.io as snapshot images, see mirroring config below
        
> Note: Openshift CI can only use Dockerfiles from the git repository. You can't create Dockerfiles on the fly during the CI run

- some of the targets in the [Makefile](https://github.com/openshift-kni/performance-addon-operators/blob/master/Makefile)
- the CI configuration per branch in the OpenshiftCI [config](https://github.com/openshift/release/tree/master/ci-operator/config/openshift-kni/performance-addon-operators) directory
- the generated CI jobs per branch in the OpenshiftCI [jobs](https://github.com/openshift/release/tree/master/ci-operator/jobs/openshift-kni/performance-addon-operators) directory
- the [image mirroring](https://github.com/openshift/release/blob/master/core-services/image-mirroring/openshift-kni/mapping_openshift-kni_quay) mapping config

All the config files in the Openshift release repository have OWNER files in their directories with people of the CNF team in it,
so they can be modified without approval of Openshift release engineers.

Documentation on how to use the config files and generate the jobs can be found in the READMEs in the release repo:
- [OpenShift Release Tooling](https://github.com/openshift/release/blob/master/README.md)  
- [CI Operator](https://github.com/openshift/release/blob/master/ci-operator/README.md)
 
## drone.io

While Openshift CI is great for running e2e tests on Openshift clusters, some other tasks are not very well supported:
- configuration of postsubmit jobs (jobs which run after a PR was merged or a git tag was pushed)
- image builds based on Dockerfiles which were created by CI

That's why also [drone.io](https://cloud.drone.io/openshift-kni/performance-addon-operators) is used for some CI tasks.
Find its documenation at [docs.drone.io](https://docs.drone.io/)

In contrast to Openshift CI, the [drone.io configuration](https://github.com/openshift-kni/performance-addon-operators/blob/master/.drone.yml) is in the operator's repository.

### Upstream releases

The first pipeline named `release` implements our [upstream release process](./releasing.md). It consists of these steps:
- build the tools image, which is used in the following steps
- build binary and generate manifests etc.
- build and push the operator image
- validate the operator image
- build and push the registry image
- validate the registry image
- create a github release

### Build tool on release branches

In order to not have to rebuild the tools image for every commit on PRs, the `build-tools` pipeline builds it on very
push/merge to release branches.

### Coverage reports

The `coverage` pipeline creates coverage reports for every PR and after every merge, and pushes results to [coveralls.io](https://coveralls.io/github/openshift-kni/performance-addon-operators)
