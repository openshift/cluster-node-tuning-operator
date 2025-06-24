package main

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"k8s.io/utils/strings/slices"

	"github.com/openshift-kni/debug-tools/pkg/cli/knit"
)

var kniEntries = []string{
	"/host/proc/cmdline",
	"/host/proc/interrupts",
	"/host/proc/irq/default_smp_affinity",
	"/host/proc/irq/*/*affinity_list",
	"/host/proc/irq/*/node",
	"/host/proc/softirqs",
	"/host/sys/devices/system/cpu/smt/active",
	"/host/proc/sys/kernel/sched_domain/cpu*/domain*/flags",
	"/host/sys/devices/system/cpu/offline",
	"/host/sys/class/dmi/id/bios*",
	"/host/sys/class/dmi/id/product_family",
	"/host/sys/class/dmi/id/product_name",
	"/host/sys/class/dmi/id/product_sku",
	"/host/sys/class/dmi/id/product_version",
}

func TestCollectMachineInfo(t *testing.T) {
	//Check if collect machine info file is created correctly
	knitOpts := &knit.KnitOptions{}
	knitOpts.SysFSRoot = "/host/sys"

	destFile := "./output"

	// Delete the file after the test
	defer os.Remove(destFile) // ignore error

	err := collectMachineinfo(knitOpts, destFile)
	if err != nil {
		t.Errorf("Collection of machine info failed: %v", err)
	}

	content, err := os.ReadFile(destFile)
	if err != nil {
		t.Errorf("Reading of generated output failed: %v", err)
	}

	output := string(content)
	if !strings.Contains(output, "timestamp") {
		t.Errorf("The generated output is not valid.")
	}
}

func TestChroot(t *testing.T) {
	entries := chrootFileSpecs(kniExpectedCloneContent(), "/host")
	if !slices.Equal(entries, kniEntries) {
		t.Errorf("The chroot file list does not match the expected value.")
	}
}

func TestSnapshot(t *testing.T) {
	knitOpts := &knit.KnitOptions{}

	opts := &snapshotOptions{}
	cmd := &cobra.Command{}
	args := []string{}

	err := makeSnapshot(cmd, knitOpts, opts, args)
	if err == nil {
		t.Errorf("Failure was expected when running without --output argument.")
	}
	t.Log(err)

	opts.output = "testSnapshot.tgz"

	// Delete the snapshot after the test
	defer os.Remove(opts.output) // ignore error

	err = makeSnapshot(cmd, knitOpts, opts, args)
	if err != nil {
		t.Errorf("Failed to collect snapshot: %v", err)
	}

	_, err = os.Stat(opts.output)
	if err != nil {
		t.Errorf("Snapshot file should have been created: %v", err)
	}
}
