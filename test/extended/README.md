# NTO QE Test Extension

> **For AI Agents**: This directory contains comprehensive documentation for AI coding assistants.
> Please read [AGENTS.md](./AGENTS.md) for detailed context about the NTO QE test framework,
> migration guidelines, suite definitions, and best practices.
>
> **Using Claude Code**: If you are using Claude Code as your AI coding assistant:
> 1. Start Claude Code from `test/extended/` directory
> 2. On first launch from a subdirectory, Claude Code will prompt you to load the parent AGENTS.md - select **Yes** (subsequent launches will auto-load)
> 3. If starting from `test/extended/` itself, AGENTS.md is automatically loaded
> 4. Use `/memory` to verify AGENTS.md is loaded and view its content
>
> This ensures Claude Code has access to test framework architecture, migration guidelines,
> suite definitions, and code quality standards.

## Implementation Strategy

### Test Case Organization

1. If the author believes a case meets OpenShift CI requirements, add the `ReleaseGate` label:
   ```go
   g.It("xxxxxx", g.Label("ReleaseGate"), func() {
   ```
   - This makes the case equivalent to origin cases for openshift-tests
   - For the cases with `ReleaseGate` that need `Informing`, add:
     ```go
     import oteg "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
     g.It("xxxxxx", g.Label("ReleaseGate"), oteg.Informing(), func() {
     ```

## Suite Definitions

### Suites for openshift-tests and PR presubmit jobs:

#### Parallel Suite
```go
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-node-tuning-operator/conformance/parallel",
		Parents: []string{"openshift/conformance/parallel"},
		Qualifiers: []string{
			`(labels.exists(l, l=="ReleaseGate")) &&
			!(name.contains("[Serial]") || name.contains("[Slow]") || name.contains("[Disruptive]"))`,
		},
	})
```

#### Serial Suite
```go
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-node-tuning-operator/conformance/serial",
		Parents: []string{"openshift/conformance/serial"},
		Qualifiers: []string{
			`(labels.exists(l, l=="ReleaseGate")) &&
			name.contains("[Serial]") && !name.contains("[Disruptive]")`,
			// refer to https://github.com/openshift/origin/blob/main/pkg/testsuites/standard_suites.go
		},
	})
```

#### Disruptive Suite
```go
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-node-tuning-operator/disruptive",
		Parents: []string{"openshift/disruptive-longrunning"},
		Qualifiers: []string{
			`name.contains("[Disruptive]")`,
		},
	})
```

#### Slow Suite
```go
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-node-tuning-operator/optional/slow",
		Parents: []string{"openshift/optional/slow"},
		Qualifiers: []string{
			`name.contains("[Slow]") && !name.contains("[Disruptive]")`,
		},
	})
```

#### All Suite
```go
	ext.AddSuite(e.Suite{
		Name: "openshift/cluster-node-tuning-operator/all",
		Qualifiers: []string{
		},
	})
```

## Test Case Migration Guide

**Required For all QE cases**:
- Do not use `&|!,()/` in case title
- Do NOT remove the test_id number from the `original-name` label. The test_id in `g.Label("original-name:...")` must include the case ID number.
  Case ID is a 5 digit number between `-` symbols.
  - ✅ **Correct**: `g.Label("[OTP][Disruptive][test_id:37415] Allow setting isolated_cores without touching the default_irq_affinity")`
  - ❌ **Wrong**: `g.Label("[OTP][Disruptive] Allow setting isolated_cores without touching the default_irq_affinity")` (missing case ID)

### A. Code Changes for Migrated Cases

All migrated test case code needs the following changes to run in the new test framework:

1. Change `compat_otp.By()` to `g.By()`
2. Change `compat_otp.XYZ()` to `utils.XYZ()`, where `XYZ` are such as `IsSNOCluster`
3. Adjust functions missing in the `utils` package from the `compat_otp` package
4. Change `ntoResource` to `NtoResource` and export its fields
5. Change `e2e.Logf()` to `utils.Logf()`
6. When using `oc.AsAdmin().WithoutNamespace()`, always use `-n` with the appropriate namespace either from the resource being created itself or NTO namespace.
   This will prevent issues in the CI when temporary OTE namespaces no longer exist.
7. Change the comments missing a space after `//`:
  - ✅ **Correct**: // test requires NTO to be installed
  - ❌ **Wrong**: //test requires NTO to be installed


### B. Label Requirements for Migrated and New Cases

#### Required Labels
1. **Component annotation**: Add `[sig-tuning-node]` in case title
2. **Jira Component**: Add `[Jira:Node Tuning Operator]` in case title
3. **OpenShift CI compatibility**: If you believe the case meets OpenShift CI requirements, add `ReleaseGate` label to Ginkgo
4. **Required For Migrated case from test-private**: Add `[OTP]` in case title

#### Optional Labels in Migration/New test cases' title
1. **Author**: Deprecated, remove it.
2. **ConnectedOnly**: Add `[Skipped:Disconnected]` in title
3. **DisconnectedOnly**: Add `[Skipped:Connected][Skipped:Proxy]` in title
4. **Case ID**: change it to `[test_id:xxxxxx]` format, and remove the old one from the case title. Such as `-37415-` strings.
   - **IMPORTANT**: The test_id number should only appear ONCE in the test title - at the beginning as `[test_id:xxxxx]`. Do NOT repeat the number anywhere else in the title.
   - **IMPORTANT**: Do NOT add `-` between two consecutive square brackets. Adjacent tags should be written directly together.
   - ✅ **Correct**: `[test_id:12345][OTP][Skipped:Disconnected]Allow setting isolated_cores without touching the default_irq_affinity [Disruptive]`
   - ❌ **Wrong**: `[test_id:12345][OTP]-[Skipped:Disconnected]Allow setting isolated_cores without touching the default_irq_affinity [Disruptive]` (dash between brackets)
   - ❌ **Wrong**: `[test_id:12345][OTP][Skipped:Disconnected]12345-Allow setting isolated_cores without touching the default_irq_affinity [Disruptive]` (repeated ID)
5. **Importance**: Deprecated, remove it. Such as `Critical`, `High`, `Medium` and `Low` strings.
6. **NonPrerelease**: Deprecated, remove it.
    - **Longduration**: Change it to `[Slow]` in case title.
    - **ChkUpg**: Deprecated, remove it. Not supported (openshift-tests upgrade differs from OpenShift QE)
7.  **VMonly**: Deprecated, and don't migrate the `VMonly` test cases to here.
8.  **Slow, Serial, Disruptive**: Preserved, but add them in the end of the title as above.
9.  **CPaasrunOnly, CPaasrunBoth, StagerunOnly, StagerunBoth, ProdrunOnly, ProdrunBoth**: Deprecated, remove them.
10. **NonHyperShiftHOST**: Use Ginkgo label `g.Label("NonHyperShiftHOST")` or use `IsHypershiftHostedCluster` judgment, then skip
11. **HyperShiftMGMT**: Deprecated. For cases needing hypershift mgmt execution, use `g.Label("NonHyperShiftHOST")` and `ValidHypershiftAndGetGuestKubeConf` validation
12. **MicroShiftOnly**: Deprecated. For cases not supporting microshift, use `SkipMicroshift` judgment, then skip
13. **ROSA**: Deprecated. Three ROSA job types:
    - `rosa-sts-ovn`: equivalent to OCP
    - `rosa-sts-hypershift-ovn`: equivalent to hypershift hosted
    - `rosa-classic-sts`: doesn't use openshift-tests
14. **ARO**: Deprecated. All ARO jobs based on HCP are equivalent to hypershift hosted (don't actually use openshift-test)
15. **OSD_CCS**: Deprecated. Only one job type: `osd-ccs-gcp` equivalent to OCP
16. **Feature Gates**: Handle test cases based on their feature gate requirements:

    **Case 1: Test only runs when feature gate is enabled**
    - The test should not execute if the feature gate is disabled
    - Add `[OCPFeatureGate:xxxx]` in `g.It` title (where xxxx is feature gate name)
    - Or use `IsFeaturegateEnabled` check, then skip if disabled
    - Remove label/check when feature no longer requires gate

    **Case 2: Test runs with/without feature gate but with different behaviors**
    - The test executes regardless of feature gate status, but behaves differently
    - Use `IsFeaturegateEnabled` check to handle different behaviors
    - Do NOT add `[OCPFeatureGate:xxxx]` label
    - Remove `IsFeaturegateEnabled` check when feature no longer requires gate

    **Case 3: Test runs with/without feature gate with same behavior**
    - The test executes the same way regardless of feature gate status
    - Do NOT use `IsFeaturegateEnabled` check
    - Do NOT add `[OCPFeatureGate:xxxx]` label
17. **Exclusive**: change to `Serial`

## Test Automation Code Requirements

Consider these requirements when writing and reviewing code:

### Security Considerations
- Does the test case generate sensitive information in logs?
- Does the code contain sensitive information in output or commands?

### Test Isolation
- Will this test case affect other test executions?
- Will this test case be affected by other test executions?

### Labeling and Cleanup
- Are correct labels applied?
- What changes does this case make to the cluster?
- Can changes be restored for both normal and abnormal exits?
- During recovery, are both actions and results correct?
- Should recovery restore to predetermined or dynamically determined values?

### Logging Best Practices
- Avoid excessive logs or large error messages
- Don't put large log outputs in error messages (use proper log messages instead). Don't use `o.Expect` to assert large messages (appears in error message on failure)
- Avoid logging `oc logs` output directly

### Code Quality
- Don't modify shared libraries (e.g., Ginkgo) or global settings affecting other tests
- Don't execute logic code in `g.Describe` except for initing oc, and move to `g.BeforeEach`
- Don't use single/double quotes in case titles (causes XML parse failures)
- Avoid `o.Expect` in `wait.Poll`:
  ```go
  // Wrong:
  wait.PollUntilContextTimeout(context.TODO(), time.Second, time.Minute, false, func(ctx context.Context) (bool, error) {
		response, err := c.AuthorizationV1().SelfSubjectAccessReviews().Create(context.Background(), review, metav1.CreateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred()) // in wait.Poll
		return response.Status.Allowed == allowed, nil
	})

  // Correct:
  wait.PollUntilContextTimeout(context.TODO(), time.Second, time.Minute, false, func(ctx context.Context) (bool, error) {
		response, err := c.AuthorizationV1().SelfSubjectAccessReviews().Create(context.Background(), review, metav1.CreateOptions{})
		if err != nil {
			return false, err
		}
		return response.Status.Allowed == allowed, nil
	})
  ```

## Local Development Workflow

### Before Submitting PR

1. **Build and compile**:
   ```bash
   make
   ```

2. **Check test name**:
   ```bash
   # List all test names and search for your test using a keyword
   _output/cluster-node-tuning-operator-test-ext list -o names | grep "keyword_from_your_test_name"
   ```

3. **Run test locally**:
   ```bash
   _output/cluster-node-tuning-operator-test-ext run-test <full test name>
   ```

4. **Test with openshift-tests**:
   - Switch to origin repo
   - Follow [test extensions documentation](https://github.com/openshift/origin/blob/main/docs/test_extensions.md)
   - Set environment variables:
     ```bash
     export OPENSHIFT_TESTS_DISABLE_CACHE=1
     export EXTENSION_BINARY_OVERRIDE_INCLUDE_TAGS=tests,cluster-node-tuning-operator
     export EXTENSION_BINARY_OVERRIDE_CLUSTER_NODE_TUNING_OPERATOR=<path to local NTO repo>/_output/cluster-node-tuning-operator-test-ext
     export EXTENSIONS_PAYLOAD_OVERRIDE=quay.io/openshift-release-dev/ocp-release:4.21.12-x86_64
     ```
   - Run appropriate suite based on your test characteristics:
     ```bash
     # Choose the suite that matches your test type:

     # For all tests:
     ./openshift-tests run openshift/cluster-node-tuning-operator/all --monitor watch-namespaces
     ```

5. **Create PR**

### PR Submission Requirements

#### Pre-submission Checks
1. Check failed presubmit jobs - verify both your new cases and whether other case failures are caused by your changes

#### Stability Testing
2. Identify release blocking jobs and run them either using `/payload-job` or `/payload-aggregate`.  For example:
   ```bash
   /payload-job periodic-ci-openshift-release-main-ci-5.0-upgrade-from-stable-4.22-e2e-gcp-ovn-rt-upgrade
   ```
