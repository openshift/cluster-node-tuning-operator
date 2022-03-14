# API versions

## Supported API Versions

The Performance Addon Operator supports *v2*, *v1* and *v1alpha1* [Performance Profile](docs/performance_profile.md) versions.
There are no differences between the *v1* and *v1alpha1* versions, the upgrade has been made in order to
mark the Performance Profile API as stable. The *v2* API introduces the new optional boolean field 
```GloballyDisableIrqLoadBalancing``` with the default value ```false```.

## Upgrade from *v1alpha1* to *v1*.
When upgrading from an older Performance Addon Operator version supporting only *v1alpha1* API to a newer one supporting
also the *v1* API, the existing *v1alpha1* Performance Profiles will be converted on-the-fly using a "None" Conversion
strategy and served to the Performance Addon Operator as *v1*.

## Upgrade from *v1alpha1* and *v1* to *v2*
When upgrading from an older Performance Addon Operator version, the existing *v1* and *v1alpha1* profiles will be converted
using a Conversion Webhook that injects the ```GloballyDisableIrqLoadBalancing``` field with the value ```true``` in order
to keep the legacy behaviour, see [Performance Profile](docs/irq-load-balancing.md).

## Q&A
What happens in practice if I install a v2-enabled PAO on a cluster? What should I expect?
- PAO will expect v2 Performance Profiles and query only them. Existing vi and v1alpha1 profiles will be served as v2 and
  PAO works with them as usual.

What happens if I submit a v1alpha1 profile, will I always get back a v2 profile?
- Any of the existent Performance Profile CRs can be retrieved as v2, v1 and v1alpha1 since the Performance Profile CRD
supports all API versions.
    For example if we have a "manual" Performance Profile in the system we can query it as v1, v2 and v1alpha1:

    ```oc get performanceprofiles.v2.performance.openshift.io manual```
    
    ```oc get performanceprofiles.v1.performance.openshift.io manual```

    ```oc get performanceprofiles.v1alpha1.performance.openshift.io manual```

    However, the Performance Addon Operator will use all the existent Performance Profiles as v2 no matter
    if they have been submitted as v1alpha1 or v1.

Where can I find more information on API versioning?
- https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definition-versioning

