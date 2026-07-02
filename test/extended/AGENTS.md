# AGENTS.md

This file provides AI agents with comprehensive context about the NTO QE Test Extension project to enable effective test development, debugging, and maintenance.

## Scope and Working Directory

### Applicability
This AGENTS.md applies to the **NTO QE Test Cases** located at:
```
cluster-node-tuning-operator/test/extended/
```

**IMPORTANT**: This file is specifically for the **QE migration test code** in the `test/extended/` directory, not for:
- Product code in the main `cluster-node-tuning-operator` repository

### Required Working Directory
For this AGENTS.md to be effective, ensure your working directory is set to:
```bash
<repo-root>/cluster-node-tuning-operator/test/extended/
```

### Working Directory Verification for AI Agents

**Context Awareness**: This AGENTS.md may be loaded even when not actively working with QE test files (e.g., user briefly left `test/extended/` for another repo path). Apply these guidelines intelligently based on the actual task.

#### When to Apply This AGENTS.md

**ONLY apply this AGENTS.md when the user is working with QE migration test files**, identified by:
- File paths containing `test/extended/`
- Tasks explicitly about "NTO QE tests", "QE migration", "test extension", "sig-tuning-node"

**DO NOT apply this AGENTS.md when**:
- Working with files outside these directories (e.g., e2e tests, product code)
- User is in a different part of the repository
- Even if this AGENTS.md was previously loaded

#### Directory Check (Only for QE Test File Operations)

When the user asks to work with QE test files (files under `test/extended/`):

1. **Check current working directory**:
   ```bash
   pwd
   ```

2. **Verify directory alignment**:
   - Preferred: Current directory should be `test/extended/` or subdirectory
   - This ensures AGENTS.md context is automatically available

3. **If working directory is not aligned**:

   **Inform (don't block) the user**:
   ```
   💡 Note: Working Directory Suggestion

   You're working with QE test files under test/extended/,
   but your current directory is elsewhere. For better context and auto-completion:

   Consider running: cd test/extended/

   I can still help you, but setting the working directory correctly
   ensures I have full access to the test documentation.

   Do you want to continue in the current directory, or should I wait
   for you to switch?
   ```

**Important**: This is a suggestion, not a blocker. If the user wants to proceed, assist them normally.

### Path Structure Reference
```
cluster-node-tuning-operator/                  ← OpenShift downstream product repo
└── test/                                      ← Test directory root
    └── extended/                              ← OpenShift Test Extension (OTE) root
        ├── bindata/                           ← Embedded test data for QE tests
        ├── specs/                             ← OTE test specifications
        ├── testdata/                          ← Raw test manifests to be compiled into bindata
        └── utils/                             ← Test helpers/utilities
```

## Project Overview

This is a **Quality Engineering (QE) test extension** for Node Tuning Operator (NTO) on OpenShift. It provides end-to-end functional tests that validate NTO features and functionality in real OpenShift clusters.

### Purpose
- Validate NTO functionality across different OpenShift topologies
- Ensure NTO works correctly in various cluster configurations (SNO, standard OCP, etc.)
- Provide regression testing for NTO bug fixes and enhancements

**Note**: NTO currently does NOT support MicroShift topology. Support may be added in future releases.

### Key Characteristics
- **Framework**: Built on Ginkgo v2 BDD testing framework and OpenShift Tests Extension (OTE)
- **Test Organization**: Polarion-ID based test case management
- **Integration**: Extends `openshift-tests-extension` framework

## Test Case Sources and Organization

**Reference**: For OpenShift CI requirements, see [Choosing a Test Suite](https://docs.google.com/document/d/1cFZj9QdzW8hbHc3H0Nce-2xrJMtpDJrwAse9H7hLiWk/edit?tab=t.0#heading=h.tjtqedd47nnu)

## Test Suite Definitions

**IMPORTANT**: Suite definitions are sourced from **[cmd/cluster-node-tuning-operator-test-ext/main.go](../../cmd/cluster-node-tuning-operator-test-ext/main.go)** and may change over time. Always refer to that file for the most current definitions.

For detailed explanations and code examples, see **[README.md](./README.md)** section "Suite Definitions".

## Test Case Migration Guide

For complete migration guidelines including code changes and label requirements, refer to **[README.md](./README.md)** section "Test Case Migration Guide".

## Test Architecture and Patterns

### Test Structure Pattern

For complete test structure examples, refer to existing test files:
- **Standard tests**: `specs/nto.go`
- **Key patterns**: Look for `g.Describe`, `g.BeforeEach`, `g.AfterEach`, `g.It` blocks

**Basic structure**:
```go
var _ = g.Describe("[Jira:Node Tuning Operator][sig-tuning-node] feature description", func() {
    defer g.GinkgoRecover()
    var oc = exutil.NewCLIWithoutNamespace("nto-test")

    g.BeforeEach(func() {
        // ensure NTO operator is installed
        utils.SkipNoNTO(oc, ntoNamespace)
        // get IaaS platform
        platformOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("infrastructure", "cluster", "-o=jsonpath={.status.platform}").Output()
        if err == nil {
            iaasPlatform = strings.ToLower(platformOutput)
        }
        utils.Logf("Cloud provider is: %v", iaasPlatform)
    })

    g.AfterEach(func() {
        // Cleanup resources (use defer)
    })

    g.It("[test_id:37415][OTP] description", g.Label("ReleaseGate"), func() {
        // Test implementation
    })
})
```

## Local Development Workflow

For complete local development workflow, build instructions, testing procedures, PR submission requirements, and disconnected environment support, refer to **[README.md](./README.md)** section "Local Development Workflow".

**Quick reference**:
- Build: `make`
- Find test: `_output/cluster-node-tuning-operator-test-ext list -o names | grep "keyword_from_your_test_name"`
- Run test: `_output/cluster-node-tuning-operator-test-ext run-test <full test name>`
- openshift-tests integration: See [README.md](./README.md) for environment variables and suite selection

**Important for Disconnected Tests**: With IDMS/ITMS in place, tests work the same in both connected and disconnected environments. See README.md for `ValidateAccessEnvironment` usage

## Test Automation Code Requirements

For complete code quality guidelines, best practices, logging best practices, and security considerations, refer to **[README.md](./README.md)** section "Test Automation Code Requirements".

**Critical rules for AI agents**:
- ✅ Use `defer` for cleanup (BEFORE resource creation): `defer resource.Delete(oc)` then `resource.Create(oc)`
- ✅ Use case ID for resource naming (NOT random strings): `name := "test-extension-" + caseID`
- ❌ Don't use `o.Expect` inside `wait.Poll` loops (use `if err != nil { return false, err }`)
- ❌ Don't execute logic in `g.Describe` blocks (only initialization, move logic to `g.BeforeEach`)
- ❌ Don't use quotes in test titles (breaks XML parsing)
- ❌ Don't put large log outputs in error messages (use proper log messages instead of `o.Expect` with large output)

## Key Utilities

For complete utility APIs and usage examples, refer to the source code and existing tests:

### `utils` Package
**Location**: `utils/` directory (e.g., `utils/cli_wrapper.go`, `utils/nto_util.go`)

**Key functions**:
- CLI management: `NewCLIWithoutNamespace()`
- Cluster detection: `IsSNOCluster()`, `IsROSACluster()`
- Skip functions: `SkipNoNTO()`

## Anti-Patterns to Avoid

For complete anti-patterns with detailed code examples and explanations, refer to **[README.md](./README.md)** section "Test Automation Code Requirements".

**Common mistakes for AI agents to avoid**:
- ❌ No cleanup: Always use `defer resource.Delete(oc)` BEFORE `resource.Create(oc)`
- ❌ Hardcoded names: Use case ID for naming: `name := "test-extension-" + caseID`
- ❌ Missing timeouts: Always specify timeout for Wait functions
- ❌ Hard sleeps: Use Wait functions instead of `time.Sleep()`
- ❌ `o.Expect` in `wait.Poll`: Use `if err != nil { return false, err }` pattern instead

## Quick Reference

### Test Naming Convention
```
[Jira:Node Tuning Operator][sig-tuning-node] should [test_id:12345][OTP]Description [Parallel|Serial|Disruptive|Slow]
```

## Resources

- [NTO OpenShift Product Code](https://github.com/openshift/cluster-node-tuning-operator)
- [Ginkgo v2 Documentation](https://onsi.github.io/ginkgo/)
- [OpenShift Tests Extension](https://github.com/openshift-eng/openshift-tests-extension)
- [Test Extensions in Origin](https://github.com/openshift/origin/blob/main/docs/test_extensions.md)
- [OpenShift CI Requirements](https://docs.google.com/document/d/1cFZj9QdzW8hbHc3H0Nce-2xrJMtpDJrwAse9H7hLiWk/edit?tab=t.0#heading=h.tjtqedd47nnu)

## Debugging

**Investigation Priority** when tests fail:
1. Check test code in `test/extended/`
2. Check resource status and conditions via `oc describe`
3. Refer to product code to understand expected behavior

**For deeper investigation** (when you need to refer to product code):
1. **Locate NTO OpenShift product code**
2. **Trace code flow**: Use the product code to understand expected behavior
3. **Compare implementation**: Check if test expectations match product implementation
4. **Check recent changes**: Look for recent commits that might have changed the behavior

**Key Namespaces** (OpenShift):
- `openshift-cluster-node-tuning-operator`: NTO operator and tuned pods

**Common Debugging Commands**:
```bash
# Check resource status
oc get tuned -n openshift-cluster-node-tuning-operator
oc get profile -n openshift-cluster-node-tuning-operator

# Check logs
oc logs -l name=cluster-node-tuning-operator -n openshift-cluster-node-tuning-operator
oc logs -l name=tuned -n openshift-cluster-node-tuning-operator
```

## Notes for AI Agents

### Suggesting Test Locations

When discussing whether a feature needs testing:

**✅ DO**: Provide simple, focused guidance on QE test placement
- Example: "If you need to write QE tests for this functionality, they should go in `test/extended/specs/`."
- Keep suggestions within the scope of this AGENTS.md (QE tests only)

**❌ DON'T**:
- Discuss DEV test locations (e.g., unit tests in product code directories)
- Explain the difference between QE and DEV tests unless explicitly asked
- Provide detailed test categorization unless the user is actively writing tests

**Remember**: This AGENTS.md is for QE test code in `test/extended/` only. Product code testing (DEV tests) is outside this scope.

### Critical Points

1. **Test Scope**:
   - This AGENTS.md applies ONLY to QE migration test code under `test/extended/`

2. **Suite Definitions Source**:
   - Always check `cmd/cluster-node-tuning-operator-test-ext/main.go` for current suite definitions
   - Suite qualifiers may change over time

3. **ReleaseGate Label Mechanism**:
   - Only `ReleaseGate` cases can be used in OpenShift General Jobs

4. **ReleaseGate is Critical**:
   - Determines if Extended case can be used in OpenShift General Jobs and PR Presubmit Jobs
   - All cases are executed via `openshift-tests` command

5. **Most Failures are Test Code Issues**:
   - Always investigate test code first before looking at product code
   - Refer to Debugging section for investigation priority

### Test Development Guidelines

1. **Component Tag**: Always use `[sig-tuning-node]`
2. **Utilities**: Use `utils` package
3. **API Focus**: Test NTO APIs (tuneds.tuned.openshift.io, performanceprofiles.performance.openshift.io)
4. **Cleanup**: Always use defer for cleanup to ensure resources are removed
5. **Suite Logic**: Understand the qualifier logic for different test suites
   - Refer to Test Suite Definitions section for suite hierarchy
   - Understand which suite your test belongs to based on labels

### Cluster Topologies

**Note**: NTO currently supports only a subset of OpenShift topologies.

**Currently Supported**:
- **Standard OCP**: Regular OpenShift clusters
- **SNO (Single Node OpenShift)**: Single-node clusters
- **HyperShift Hosted**: Hosted control plane clusters
- **HyperShift Management**: Management clusters for hosted control planes

**NOT Currently Supported**:
- **MicroShift**: Lightweight OpenShift for edge (not yet supported by NTO)

**Network Connectivity**:
- **Connected**: Full internet access
- **Disconnected**: No internet access (air-gapped)
- **Proxy**: Internet access through proxy

### Common Pitfalls

**Test Code Issues**:
1. ❌ **Don't** use `o.Expect` inside `wait.Poll` loops (causes panic)
2. ❌ **Don't** use quotes in test titles (breaks XML parsing)
3. ❌ **Don't** execute logic in `g.Describe` blocks (only initialization)
4. ❌ **Don't** add `ReleaseGate` to `[Disruptive]` and `[Slow]` cases
5. ❌ **Don't** forget cleanup in `g.AfterEach` with defer

### Best Practices

**General Test Practices**:
1. ✅ **Do** check suite definitions in `cmd/cluster-node-tuning-operator-test-ext/main.go` before adding tests
2. ✅ **Do** use case ID for naming resources (NOT random strings)
3. ✅ **Do** add proper test_id (Polarion ID) to all test cases
4. ✅ **Do** use skip functions for topology-specific tests
5. ✅ **Do** register defer cleanup BEFORE creating resources
   - Pattern: `defer resource.Delete(oc)` then `resource.Create(oc)`
   - Why: Ensures cleanup even if Create partially succeeds then fails
6. ✅ **Do** test locally with `cluster-node-tuning-operator-test-ext` before submitting PR
7. ✅ **Do** test with `openshift-tests` to verify suite selection
8. ✅ **Do** run stability tests (`/payload-job`) for ReleaseGate test cases

### Build and Run

For complete workflow and detailed commands, refer to **[README.md](./README.md)** section "Local Development Workflow" and the **[Quick Reference](#quick-reference)** section above.

**Essential pattern for AI agents**:
1. Build: `make`
2. Find test: `_output/cluster-node-tuning-operator-test-ext list -o names | grep <keyword>`
3. Run locally: `_output/cluster-node-tuning-operator-test-ext run-test "<full test name>"`
4. Test with openshift-tests: See README.md for environment variables and suite selection
5. Run stability tests: `/payload-job` for ReleaseGate test cases (see README.md for details)
