package __latency_testing

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
)

// TODO get commonly used variables from one shared file that defines constants
const (
	testExecutablePath = "../../../../../build/_output/bin/latency-e2e.test"
	//tool to test
	oslat       = "oslat"
	cyclictest  = "cyclictest"
	hwlatdetect = "hwlatdetect"
	//Environment variables names
	latencyTestDelay     = "LATENCY_TEST_DELAY"
	latencyTestRun       = "LATENCY_TEST_RUN"
	latencyTestRuntime   = "LATENCY_TEST_RUNTIME"
	maximumLatency       = "MAXIMUM_LATENCY"
	oslatMaxLatency      = "OSLAT_MAXIMUM_LATENCY"
	hwlatdetecMaxLatency = "HWLATDETECT_MAXIMUM_LATENCY"
	cyclictestMaxLatency = "CYCLICTEST_MAXIMUM_LATENCY"
	latencyTestCpus      = "LATENCY_TEST_CPUS"
	//invalid values error messages
	unexpectedError = "Unexpected error"
	//incorrect values error messages
	incorrectMsgPart1                  = "the environment variable "
	incorrectMsgPart2                  = " has incorrect value"
	invalidNumber                      = " has an invalid number"
	maxInt                             = "2147483647"
	minimumCpuForOslat                 = "2"
	mustBePositiveInt                  = ".*it must be a positive integer with maximum value of " + maxInt
	mustBeNonNegativeInt               = ".*it must be a non-negative integer with maximum value of " + maxInt
	incorrectCpuNumber                 = incorrectMsgPart1 + latencyTestCpus + incorrectMsgPart2 + mustBePositiveInt
	invalidCpuNumber                   = incorrectMsgPart1 + latencyTestCpus + invalidNumber + mustBePositiveInt
	incorrectDelay                     = incorrectMsgPart1 + latencyTestDelay + incorrectMsgPart2 + mustBeNonNegativeInt
	invalidNumberDelay                 = incorrectMsgPart1 + latencyTestDelay + invalidNumber + mustBeNonNegativeInt
	incorrectMaxLatency                = incorrectMsgPart1 + maximumLatency + incorrectMsgPart2 + mustBeNonNegativeInt
	invalidNumberMaxLatency            = incorrectMsgPart1 + maximumLatency + invalidNumber + mustBeNonNegativeInt
	incorrectOslatMaxLatency           = incorrectMsgPart1 + "\"" + oslatMaxLatency + "\"" + incorrectMsgPart2 + mustBeNonNegativeInt
	invalidNumberOslatMaxLatency       = incorrectMsgPart1 + "\"" + oslatMaxLatency + "\"" + invalidNumber + mustBeNonNegativeInt
	incorrectCyclictestMaxLatency      = incorrectMsgPart1 + "\"" + cyclictestMaxLatency + "\"" + incorrectMsgPart2 + mustBeNonNegativeInt
	invalidNumberCyclictestMaxLatency  = incorrectMsgPart1 + "\"" + cyclictestMaxLatency + "\"" + invalidNumber + mustBeNonNegativeInt
	incorrectHwlatdetectMaxLatency     = incorrectMsgPart1 + "\"" + hwlatdetecMaxLatency + "\"" + incorrectMsgPart2 + mustBeNonNegativeInt
	invalidNumberHwlatdetectMaxLatency = incorrectMsgPart1 + "\"" + hwlatdetecMaxLatency + "\"" + invalidNumber + mustBeNonNegativeInt
	incorrectTestRun                   = incorrectMsgPart1 + latencyTestRun + incorrectMsgPart2
	incorrectRuntime                   = incorrectMsgPart1 + latencyTestRuntime + incorrectMsgPart2 + mustBePositiveInt
	invalidNumberRuntime               = incorrectMsgPart1 + latencyTestRuntime + invalidNumber + mustBePositiveInt
	//success messages regex
	success = `SUCCESS.*1 Passed.*0 Failed.*2 Skipped`
	//failure messages regex
	latencyFail = `The current latency .* is bigger than the expected one`
	fail        = `FAIL.*0 Passed.*1 Failed.*2 Skipped`
	//hwlatdetect fail message regex
	hwlatdetectFail = `Samples exceeding threshold: [^0]`
	//skip messages regex
	skipTestRun         = `Skip the latency test, the LATENCY_TEST_RUN set to false`
	skipMaxLatency      = `no maximum latency value provided, skip buckets latency check`
	skipOslatCpuNumber  = `Skip the oslat test, LATENCY_TEST_CPUS is less than the minimum CPUs amount ` + minimumCpuForOslat
	skip                = `SUCCESS.*0 Passed.*0 Failed.*3 Skipped`
	skipInsufficientCpu = `Insufficient cpu to run the test`
	skipOddCpuNumber    = `Skip the test, the requested number of CPUs should be even to avoid noisy neighbor situation`

	//used values parameters

	// we do not care about the actual system latency because CI systems are not tuned well enough to be an example for
	// latency measuring, besides this suite only cares about testing the test executable with different values of env vars.
	untunedLatencyThreshold = "10000000" //10s
	negativeTesting         = false
	positiveTesting         = true
)

// Struct to hold each test parameters
type latencyTest struct {
	testDelay             string
	testRun               string
	testRuntime           string
	testMaxLatency        string
	oslatMaxLatency       string
	cyclictestMaxLatency  string
	hwlatdetectMaxLatency string
	testCpus              string
	outputMsgs            []string
	toolToTest            string
}

var _ = DescribeTable("Test latency measurement tools tests", func(testGroup []latencyTest, isPositiveTest bool) {
	format.MaxLength = 0
	for _, test := range testGroup {
		clearEnv()
		testDescription := setEnvAndGetDescription(test)
		By(testDescription)
		output, err := exec.Command(testExecutablePath, "-ginkgo.v", "-ginkgo.focus", test.toolToTest).Output()
		if err != nil {
			//we don't log Error level here because the test might be a negative check
			testlog.Info(err.Error())
		}

		ok, matchErr := regexp.MatchString(skipInsufficientCpu, string(output))
		if matchErr != nil {
			testlog.Error(matchErr.Error())
		}
		if ok {
			testlog.Info(skipInsufficientCpu)
			continue
		}

		if isPositiveTest {
			if err != nil {
				testlog.Error(err.Error())
			}
			Expect(string(output)).NotTo(MatchRegexp(unexpectedError), "Unexpected error was detected in a positive test")
			//Check runtime argument in the pod's log only if the tool is expected to be executed
			ok, matchErr := regexp.MatchString(success, string(output))
			if matchErr != nil {
				testlog.Error(matchErr.Error())
			}
			if ok {
				//verify the command is executed with the expected args
				//this lists of args depend on the ones the latency tool runners adds to tool command in cnf-features-deploy.
				var passedArgs []string
				switch test.toolToTest {
				case oslat:
					passedArgs = []string{"--duration " + test.testRuntime, "--rtprio ", "--cpu-list ", "--cpu-main-thread "}
				case cyclictest:
					passedArgs = []string{"--duration " + test.testRuntime, "--priority 95", "--threads ", "--affinity ", "--histogram ", "--interval ", "--mlockall ", "--quiet"}
				case hwlatdetect:
					thr := test.testMaxLatency
					if test.hwlatdetectMaxLatency != "" {
						thr = test.hwlatdetectMaxLatency
					}
					passedArgs = []string{"--duration " + test.testRuntime, "--threshold " + thr, "--hardlimit " + thr, "--window ", "--width "}
				default:
					testlog.Error("the tool to test was not set")
				}

				for _, argument := range passedArgs {
					Expect(strings.Contains(string(output), argument)).To(BeTrue(), "The tool command didn't pass the argument %q", argument)
				}
			}
		}
		for _, msg := range test.outputMsgs {
			Expect(string(output)).To(MatchRegexp(msg), "The output of the executed tool is not as expected")
		}
	}
},
	Entry("[test_id:42851] Latency tools shouldn't run with default environment variables values", []latencyTest{{outputMsgs: []string{skip, skipTestRun}}}, positiveTesting),
	Entry("[test_id:42850] Oslat - Verify that the tool is working properly with valid environment variables values", getValidValuesTests(oslat), positiveTesting),
	Entry("[test_id:42853] Oslat - Verify that the latency tool test should print an expected error message when passing invalid environment variables values", getNegativeTests(oslat), negativeTesting),
	Entry("[test_id:42115] Cyclictest - Verify that the tool is working properly with valid environment variables values", getValidValuesTests(cyclictest), positiveTesting),
	Entry("[test_id:42852] Cyclictest - Verify that the latency tool test should print an expected error message when passing invalid environment variables values", getNegativeTests(cyclictest), negativeTesting),
	Entry("[test_id:42849] Hwlatdetect - Verify that the tool is working properly with valid environment variables values", getValidValuesTests(hwlatdetect), positiveTesting),
	Entry("[test_id:42856] Hwlatdetect - Verify that the latency tool test should print an expected error message when passing invalid environment variables values", getNegativeTests(hwlatdetect), negativeTesting),
)

func setEnvAndGetDescription(tst latencyTest) string {
	sb := bytes.NewBufferString("")
	testName := tst.toolToTest
	if tst.toolToTest == "" {
		testName = "latency tools"
	}
	fmt.Fprintf(sb, "Run %s test : \n", testName)
	nonDefaultValues := false
	if tst.testDelay != "" {
		setEnvWriteDescription(latencyTestDelay, tst.testDelay, sb, &nonDefaultValues)
	}
	if tst.testRun != "" {
		setEnvWriteDescription(latencyTestRun, tst.testRun, sb, &nonDefaultValues)
	}
	if tst.testRuntime != "" {
		setEnvWriteDescription(latencyTestRuntime, tst.testRuntime, sb, &nonDefaultValues)
	}
	if tst.testMaxLatency != "" {
		setEnvWriteDescription(maximumLatency, tst.testMaxLatency, sb, &nonDefaultValues)
	}
	if tst.oslatMaxLatency != "" {
		setEnvWriteDescription(oslatMaxLatency, tst.oslatMaxLatency, sb, &nonDefaultValues)
	}
	if tst.cyclictestMaxLatency != "" {
		setEnvWriteDescription(cyclictestMaxLatency, tst.cyclictestMaxLatency, sb, &nonDefaultValues)
	}
	if tst.hwlatdetectMaxLatency != "" {
		setEnvWriteDescription(hwlatdetecMaxLatency, tst.hwlatdetectMaxLatency, sb, &nonDefaultValues)
	}
	if tst.testCpus != "" {
		setEnvWriteDescription(latencyTestCpus, tst.testCpus, sb, &nonDefaultValues)
	}
	if !nonDefaultValues {
		fmt.Fprint(sb, "With default values of the environment variables")
	}

	return sb.String()
}

func setEnvWriteDescription(envVar string, val string, sb *bytes.Buffer, flag *bool) {
	os.Setenv(envVar, val)
	fmt.Fprintf(sb, "%s = %s \n", envVar, val)
	*flag = true
}

func clearEnv() {
	os.Unsetenv(latencyTestDelay)
	os.Unsetenv(latencyTestRun)
	os.Unsetenv(latencyTestRuntime)
	os.Unsetenv(maximumLatency)
	os.Unsetenv(oslatMaxLatency)
	os.Unsetenv(cyclictestMaxLatency)
	os.Unsetenv(hwlatdetecMaxLatency)
	os.Unsetenv(latencyTestCpus)
}

func getValidValuesTests(toolToTest string) []latencyTest {
	var testSet []latencyTest

	//testRuntime: for tests with success message (hence anticipated to run the tools),let runtime be 30 seconds for most of the tests for two reasons:
	//1. to let the tools have their time to measure latency properly 2. have time to check that the pod phase turned running and not completed immediately
	//testCpus: for tests that expect a success output message, note that an even CPU number is needed, otherwise the test would fail with SMTAlignmentError

	successRuntime := "30"
	testSet = append(testSet, latencyTest{testDelay: "200", testRun: "true", testRuntime: successRuntime, testMaxLatency: untunedLatencyThreshold, testCpus: "4", outputMsgs: []string{success}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testDelay: "0", testRun: "true", testRuntime: successRuntime, testMaxLatency: untunedLatencyThreshold, testCpus: "4", outputMsgs: []string{success}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testDelay: "0", testRun: "true", testRuntime: successRuntime, testMaxLatency: untunedLatencyThreshold, testCpus: "6", outputMsgs: []string{success}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testDelay: "1", testRun: "true", testRuntime: successRuntime, testMaxLatency: untunedLatencyThreshold, outputMsgs: []string{success}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testDelay: "60", testRun: "true", testRuntime: successRuntime, testMaxLatency: untunedLatencyThreshold, outputMsgs: []string{success}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", testCpus: "5", testMaxLatency: untunedLatencyThreshold, outputMsgs: []string{skip, skipOddCpuNumber}, toolToTest: toolToTest})

	if toolToTest != hwlatdetect {
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "1", outputMsgs: []string{skip, skipMaxLatency}, toolToTest: toolToTest})
	}
	if toolToTest == oslat {
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: successRuntime, testMaxLatency: "1", oslatMaxLatency: untunedLatencyThreshold, outputMsgs: []string{success}, toolToTest: toolToTest})
		//TODO add tests when requested cpus for oslat is 2 once BZ 2055267 is resolved
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: successRuntime, oslatMaxLatency: untunedLatencyThreshold, outputMsgs: []string{success}, toolToTest: toolToTest})
		//TODO: update isolated CPUs in PP to 1 and restore the original set post test
	}
	if toolToTest == cyclictest {
		//TODO add tests when requested cpus for cyclictest is 2 or less once BZ 2094046 is resolved
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: successRuntime, testMaxLatency: "1", cyclictestMaxLatency: untunedLatencyThreshold, outputMsgs: []string{success}, toolToTest: toolToTest})
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: successRuntime, cyclictestMaxLatency: untunedLatencyThreshold, outputMsgs: []string{success}, toolToTest: toolToTest})
	}
	if toolToTest == hwlatdetect {
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: successRuntime, testMaxLatency: "1", hwlatdetectMaxLatency: untunedLatencyThreshold, outputMsgs: []string{success}, toolToTest: toolToTest})
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: successRuntime, hwlatdetectMaxLatency: untunedLatencyThreshold, outputMsgs: []string{success}, toolToTest: toolToTest})
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: successRuntime, testMaxLatency: untunedLatencyThreshold, outputMsgs: []string{success}, toolToTest: toolToTest})
	}
	return testSet
}

func getNegativeTests(toolToTest string) []latencyTest {
	var testSet []latencyTest
	latencyFailureMsg := latencyFail
	if toolToTest == hwlatdetect {
		latencyFailureMsg = hwlatdetectFail
	}

	testSet = append(testSet, latencyTest{testDelay: "0", testRun: "true", testRuntime: "5", testMaxLatency: "1", outputMsgs: []string{latencyFailureMsg, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "yes", testRuntime: "5", testMaxLatency: "1", outputMsgs: []string{incorrectTestRun, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "true", testRuntime: fmt.Sprint(math.MaxInt32 + 1), outputMsgs: []string{invalidNumberRuntime, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "-1", testMaxLatency: "1", outputMsgs: []string{invalidNumberRuntime, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "5", testMaxLatency: "-2", outputMsgs: []string{invalidNumberMaxLatency, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "1H", outputMsgs: []string{incorrectRuntime, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", testMaxLatency: "&", outputMsgs: []string{incorrectMaxLatency, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", testMaxLatency: fmt.Sprint(math.MaxInt32 + 1), outputMsgs: []string{invalidNumberMaxLatency, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testDelay: "J", testRun: "true", outputMsgs: []string{incorrectDelay, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testDelay: fmt.Sprint(math.MaxInt32 + 1), testRun: "true", outputMsgs: []string{invalidNumberDelay, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testDelay: "-5", testRun: "true", outputMsgs: []string{invalidNumberDelay, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", testMaxLatency: "1", testCpus: "p", outputMsgs: []string{incorrectCpuNumber, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", testMaxLatency: "1", testCpus: fmt.Sprint(math.MaxInt32 + 1), outputMsgs: []string{invalidCpuNumber, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", testCpus: "-1", outputMsgs: []string{invalidCpuNumber, fail}, toolToTest: toolToTest})
	testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", testCpus: "0", outputMsgs: []string{invalidCpuNumber, fail}, toolToTest: toolToTest})
	if toolToTest == oslat {
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", oslatMaxLatency: "&", outputMsgs: []string{incorrectOslatMaxLatency, fail}, toolToTest: toolToTest})
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", oslatMaxLatency: fmt.Sprint(math.MaxInt32 + 1), outputMsgs: []string{invalidNumberOslatMaxLatency, fail}, toolToTest: toolToTest})
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", oslatMaxLatency: "-3", outputMsgs: []string{invalidNumberOslatMaxLatency, fail}, toolToTest: toolToTest})
	}
	if toolToTest == cyclictest {
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", cyclictestMaxLatency: "&", outputMsgs: []string{incorrectCyclictestMaxLatency, fail}, toolToTest: toolToTest})
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", cyclictestMaxLatency: fmt.Sprint(math.MaxInt32 + 1), outputMsgs: []string{invalidNumberCyclictestMaxLatency, fail}, toolToTest: toolToTest})
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", cyclictestMaxLatency: "-3", outputMsgs: []string{invalidNumberCyclictestMaxLatency, fail}, toolToTest: toolToTest})
	}
	if toolToTest == hwlatdetect {
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", hwlatdetectMaxLatency: "&", outputMsgs: []string{incorrectHwlatdetectMaxLatency, fail}, toolToTest: toolToTest})
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", hwlatdetectMaxLatency: fmt.Sprint(math.MaxInt32 + 1), outputMsgs: []string{invalidNumberHwlatdetectMaxLatency, fail}, toolToTest: toolToTest})
		testSet = append(testSet, latencyTest{testRun: "true", testRuntime: "2", hwlatdetectMaxLatency: "-3", outputMsgs: []string{invalidNumberHwlatdetectMaxLatency, fail}, toolToTest: toolToTest})
	}
	return testSet
}
