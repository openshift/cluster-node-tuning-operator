# CI

## OpenshiftCI

[OpenshiftCI](https://github.com/openshift/release) is used for running unit tests, building container images and
running e2e tests. The building blocks for the complete Ci configuration are:

- some of the targets in the [Makefile](https://github.com/openshift/cluster-node-tuning-operator/blob/master/Makefile)
- the CI configuration per branch in the OpenshiftCI [config](https://github.com/openshift/release/tree/master/ci-operator/config/openshift/cluster-node-tuning-operator) directory
- the generated CI jobs per branch in the OpenshiftCI [jobs](https://github.com/openshift/release/tree/master/ci-operator/jobs/openshift/cluster-node-tuning-operator) directory

All the config files in the Openshift release repository have OWNER files in their directories with people of the Performance team in it,
so they can be modified without approval of Openshift release engineers.

Documentation on how to use the config files and generate the jobs can be found in the READMEs in the release repo:
- [OpenShift Release Tooling](https://github.com/openshift/release/blob/master/README.md)  
- [CI Operator](https://github.com/openshift/release/blob/master/ci-operator/README.md)


