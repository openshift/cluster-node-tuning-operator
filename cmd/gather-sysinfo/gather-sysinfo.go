package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/jaypipes/ghw/pkg/snapshot"
	"github.com/openshift-kni/debug-tools/pkg/cli/knit"
	"github.com/openshift-kni/debug-tools/pkg/cli/knit/k8s"
	"github.com/openshift-kni/debug-tools/pkg/machineinformer"
	"github.com/spf13/cobra"
)

const machineInfoFilePath string = "machineinfo.json"

type snapshotOptions struct {
	dumpList bool
	output   string
	rootDir  string
}

func main() {
	root := knit.NewRootCommand(newSnapshotCommand,
		k8s.NewPodResourcesCommand,
		k8s.NewPodInfoCommand,
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func newSnapshotCommand(knitOpts *knit.KnitOptions) *cobra.Command {
	opts := &snapshotOptions{}
	snap := &cobra.Command{
		Use:   "snapshot",
		Short: "snapshot pseudofilesystems for offline analysis",
		RunE: func(cmd *cobra.Command, args []string) error {
			return makeSnapshot(cmd, knitOpts, opts, args)
		},
		Args: cobra.NoArgs,
	}
	snap.Flags().StringVar(&opts.rootDir, "root", "", "pseudofs root - use this if running inside a container")
	snap.Flags().StringVar(&opts.output, "output", "", "path to clone system information into")
	snap.Flags().BoolVar(&opts.dumpList, "dump", false, "just dump the glob list of expected content and exit")

	return snap
}

func collectMachineinfo(knitOpts *knit.KnitOptions, destPath string) error {
	outfile, err := os.Create(destPath)
	if err != nil {
		return err
	}

	mih := machineinformer.Handle{
		RootDirectory: knitOpts.SysFSRoot,
		Out:           outfile,
	}
	mih.Run()

	return nil
}

func makeSnapshot(cmd *cobra.Command, knitOpts *knit.KnitOptions, opts *snapshotOptions, args []string) error {
	fileSpecs := dedupExpectedContent(kniExpectedCloneContent(), snapshot.ExpectedCloneContent())
	if opts.dumpList {
		for _, fileSpec := range fileSpecs {
			fmt.Printf("%s\n", fileSpec)
		}
		return nil
	}

	if opts.output == "" {
		return fmt.Errorf("--output is required")
	}

	if knitOpts.Debug {
		snapshot.SetTraceFunction(func(msg string, args ...interface{}) {
			knitOpts.Log.Printf(msg, args...)
		})
	}

	scratchDir, err := os.MkdirTemp("", "perf-must-gather-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratchDir)

	if opts.rootDir != "" {
		fileSpecs = chrootFileSpecs(fileSpecs, opts.rootDir)
	}

	if err := snapshot.CopyFilesInto(fileSpecs, scratchDir, nil); err != nil {
		return fmt.Errorf("error cloning extra files into %q: %v", scratchDir, err)
	}

	if opts.rootDir != "" {
		scratchDir = filepath.Join(scratchDir, opts.rootDir)
	}

	// intentionally ignore errors, keep collecting data
	// machineinfo data is accessory, if we fail to collect
	// we want to keep going and try to collect /proc and /sys data,
	// which is more important
	localPath := filepath.Join(scratchDir, machineInfoFilePath)
	err = collectMachineinfo(knitOpts, localPath)
	if err != nil {
		log.Printf("error collecting machineinfo data: %v - continuing", err)
	}

	dest := opts.output
	if dest == "-" {
		err = snapshot.PackWithWriter(os.Stdout, scratchDir)
		dest = "stdout"
	} else {
		err = snapshot.PackFrom(dest, scratchDir)
	}
	if err != nil {
		return fmt.Errorf("error packing %q to %q: %v", scratchDir, dest, err)
	}

	return nil
}

func chrootFileSpecs(fileSpecs []string, root string) []string {
	var entries []string
	for _, fileSpec := range fileSpecs {
		entries = append(entries, filepath.Join(root, fileSpec))
	}
	return entries
}

// realpath resolves all symbolic links in the given pathname and returns the canonical path.
// Return the resolved pathname or an error (e.g. too many links) if the path fails to resolve.
func realpath(path string) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("empty path provided")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to convert to absolute path: %w", err)
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve symbolic links: %w", err)
	}

	return filepath.Clean(resolvedPath), nil
}

// Deduplication in dedupExpectedContent() is not needed, but does not hurt.
// However, it is important to feed ghw's CopyFilesInto() directory entries
// closer to filesystem root prior to directory entries farther from the root.
// Otherwise, ghw as of v0.15.0 will not handle this well.  Illustration.
// Consider we have the following file specs:
// 1) "/sys/devices/system/node/node0/cpu1/online"
// 2) "/sys/devices/system/node/node0/cpu1/"
// also consider that "/sys/devices/system/node/node0/cpu1" is a symbolic
// link pointing to "../../cpu/cpu1".  Then, ghw's CopyFilesInto() will create
// directory (instead of a link) "/sys/devices/system/node/node0/cpu1" and add
// a regular file "online".  This is wrong, because we prefer the symbolic link
// as it points to a directory which has many more entries.  The simplest fix for
// this behaviour is to sort the file specs, so that the shorter entries come
// first, i.e.:
// 1) "/sys/devices/system/node/node0/cpu1/"
// 2) "/sys/devices/system/node/node0/cpu1/online"
func dedupExpectedContent(fileSpecs, extraFileSpecs []string) []string {
	realpathSet := make(map[string]int)
	secondarySet := make(map[string]int)

	fileSpecs = append(fileSpecs, extraFileSpecs...)

	for _, fileSpec := range fileSpecs {
		matches, err := filepath.Glob(fileSpec)
		if err != nil {
			// Glob ignores file system errors such as I/O errors reading directories.
			// The only possible returned error is when fileSpec is malformed.
			log.Printf("fileSpec %q likely malformed, continuing: %v", fileSpec, err)
			continue
		}
		for _, match := range matches {
			realPath, err := realpath(match)
			if err != nil {
				// We failed to resolve the real path of "match".  Keep the "match" in the secondary set.
				secondarySet[match]++
			} else {
				// "match" resolved to path fine.
				if realPath == match {
					realpathSet[match]++
				}
			}
		}
		for _, match := range matches {
			// Keep the unresolved entries too, but in the secondary set.
			if realpathSet[match] == 0 {
				// We don't have this spec in the resolved set, add it.
				secondarySet[match]++
			}
		}
	}

	var retSpecs, secondarySpecs []string
	for entry := range realpathSet {
		// Create resolved entries first.
		retSpecs = append(retSpecs, entry)
	}
	for entry := range secondarySet {
		secondarySpecs = append(secondarySpecs, entry)
	}
	sort.Strings(secondarySpecs)
	retSpecs = append(retSpecs, secondarySpecs...)

	return retSpecs
}

func kniExpectedCloneContent() []string {
	return []string{
		// generic information
		"/proc/cmdline",
		// IRQ affinities
		"/proc/interrupts",
		"/proc/irq/default_smp_affinity",
		"/proc/irq/*/*affinity_list",
		"/proc/irq/*/node",
		// softirqs counters
		"/proc/softirqs",
		// KNI-specific CPU infos:
		"/sys/devices/system/cpu/smt/active",
		"/proc/sys/kernel/sched_domain/cpu*/domain*/flags",
		"/sys/devices/system/cpu/offline",
		// BIOS/firmware versions
		"/sys/class/dmi/id/bios*",
		"/sys/class/dmi/id/product_family",
		"/sys/class/dmi/id/product_name",
		"/sys/class/dmi/id/product_sku",
		"/sys/class/dmi/id/product_version",
	}
}
