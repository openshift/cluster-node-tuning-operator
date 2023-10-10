package tuned

import (
	"bufio"   // scanner
	"bytes"   // bytes.Buffer
	"context" // context.TODO()
	"flag"    // command-line options parsing
	"fmt"     // Printf()
	"math"    // math.Pow()
	"os"      // os.Exit(), os.Stderr, ...
	"os/exec" // os.Exec()
	"strconv" // strconv
	"strings" // strings.Join()
	"syscall" // syscall.SIGHUP, ...
	"time"    // time.Second, ...

	fsnotify "gopkg.in/fsnotify.v1"
	"gopkg.in/ini.v1"
	corev1 "k8s.io/api/core/v1"
	kmeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	tunedset "github.com/openshift/cluster-node-tuning-operator/pkg/generated/clientset/versioned"
	tunedinformers "github.com/openshift/cluster-node-tuning-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/openshift/cluster-node-tuning-operator/version"
)

// Constants
const (
	// Constants used for instantiating Profile status conditions;
	// they will be set to 2^0, 2^1, 2^2, ..., 2^n
	scApplied Bits = 1 << iota
	scWarn
	scError
	scSysctlOverride
	scTimeout
	scUnknown
)

// Constants
const (
	operandNamespace       = "openshift-cluster-node-tuning-operator"
	programName            = version.OperandFilename
	tunedProfilesDirCustom = "/etc/tuned"
	tunedProfilesDirSystem = "/usr/lib/tuned"
	tunedConfFile          = "tuned.conf"
	tunedMainConfFile      = "tuned-main.conf"
	tunedActiveProfileFile = tunedProfilesDirCustom + "/active_profile"
	tunedRecommendDir      = tunedProfilesDirCustom + "/recommend.d"
	tunedRecommendFile     = tunedRecommendDir + "/50-openshift.conf"
	tunedBootcmdlineEnvVar = "TUNED_BOOT_CMDLINE"
	tunedBootcmdlineFile   = tunedProfilesDirCustom + "/bootcmdline"
	// A couple of seconds should be more than enough for TuneD daemon to gracefully stop;
	// be generous and give it 10s.
	tunedGracefulExitWait = time.Second * time.Duration(10)
	// TuneD profile application typically takes ~0.5s and should never take more than ~5s.
	// However, there were cases where TuneD daemon got stuck during application of a profile.
	// Experience shows that subsequent restarts of TuneD can resolve this in certain situations,
	// but not in others -- an extreme example is a TuneD profile including a profile that does
	// not exist.  TuneD itself has no mechanism for restarting a profile application that takes
	// too long.  The tunedTimeout below is time to wait for "profile applied/reload failed" from
	// TuneD logs before restarting TuneD and thus retrying the profile application.  Keep this
	// reasonably low to workaround system/TuneD issues as soon as possible, but not too low
	// to increase the system load by retrying profile applications that can never succeed.
	openshiftTunedHome     = "/var/lib/tuned"
	openshiftTunedRunDir   = "/run/" + programName
	openshiftTunedPidFile  = openshiftTunedRunDir + "/" + programName + ".pid"
	openshiftTunedProvider = openshiftTunedHome + "/provider"
	tunedInitialTimeout    = 60 // timeout in seconds
	// With the less aggressive rate limiter, retries will happen at 100ms*2^(retry_n-1):
	// 100ms, 200ms, 400ms, 800ms, 1.6s, 3.2s, 6.4s, 12.8s, 25.6s, 51.2s, 102.4s, 3.4m, 6.8m, 13.7m, 27.3m
	maxRetries = 15
	// workqueue related constants
	wqKindDaemon  = "daemon"
	wqKindTuned   = "tuned"
	wqKindProfile = "profile"
)

// Types
type Bits uint8

type Controller struct {
	kubeconfig *restclient.Config
	kubeclient kubernetes.Interface

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens.
	wqKube  workqueue.RateLimitingInterface
	wqTuneD workqueue.RateLimitingInterface

	listers *ntoclient.Listers
	clients *ntoclient.Clients

	change struct {
		// Did the node Profile k8s object change?
		profile bool
		// Did the "rendered" Tuned k8s object change?
		rendered bool
		// Did tunedBootcmdlineFile change on the filesystem?
		// It is set to false on successful Profile update.
		bootcmdline bool
		// Did the command-line parameters to run the TuneD daemon change?
		// In other words, is a complete restart of the TuneD daemon needed?
		daemon bool
	}

	daemon struct {
		// reloading is true during the TuneD daemon reload.
		reloading bool
		// reloaded is true immediately after the TuneD daemon finished reloading.
		// and the node Profile k8s object's Status needs to be set for the operator;
		// it is set to false on successful Profile update.
		reloaded bool
		// debugging flag
		debug bool
		// bit/set representaton of Profile status conditions to report back via API.
		status Bits
		// stderr log from TuneD daemon to report back via API.
		stderr string
		// stopping is true while the controller tries to stop the TuneD daemon.
		stopping bool
		// the TuneD profile we wish to be applied.
		recommendedProfile string
	}

	tunedCmd     *exec.Cmd       // external command (tuned) being prepared or run
	tunedExit    chan bool       // bi-directional channel to signal and register TuneD daemon exit
	stopCh       <-chan struct{} // receive-only channel to stop the openshift-tuned controller
	changeCh     chan bool       // bi-directional channel to wake-up the main thread to process accrued changes
	changeChRet  chan bool       // bi-directional channel to announce success/failure of change processing
	tunedTicker  *time.Ticker    // ticker that fires if TuneD daemon fails to report "profile applied/reload failed" within tunedTimeout
	tunedTimeout int             // timeout for TuneD daemon to report "profile applied/reload failed" [s]
	tunedMainCfg *ini.File       // global TuneD configuration as defined in tuned-main.conf
}

type wqKey struct {
	kind string // object kind
	name string // object name
	//nolint:unused
	event string // object event type (add/update/delete) or pass the full object on delete
}

func parseCmdOpts() {
	klog.InitFlags(nil)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", programName)
		fmt.Fprintf(os.Stderr, "Example: %s\n\n", programName)
		fmt.Fprintf(os.Stderr, "Options:\n")

		flag.PrintDefaults()
	}

	flag.Parse()
}

// Get a client from kubelet's kubeconfig to write to the Node object.
func getKubeClient() (kubernetes.Interface, error) {
	var (
		// paths to kubelet kubeconfig
		kubeletKubeconfigPaths = []string{
			"/var/lib/kubelet/kubeconfig",        // traditional OCP
			"/etc/kubernetes/kubelet-kubeconfig", // IBM Managed OpenShift, see OCPBUGS-19795
		}
		kubeletKubeconfigPath string
	)

	for _, kubeletKubeconfigPath = range kubeletKubeconfigPaths {
		if _, err := os.Stat(kubeletKubeconfigPath); err == nil {
			break
		}
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeletKubeconfigPath)
	if err != nil {
		return nil, err
	}
	kubeClient, err := kubernetes.NewForConfig(restclient.AddUserAgent(config, programName))
	if err != nil {
		return nil, err
	}

	return kubeClient, nil
}

func newController(stopCh <-chan struct{}) (*Controller, error) {
	kubeconfig, err := ntoclient.GetConfig()
	if err != nil {
		return nil, err
	}
	kubeclient, err := getKubeClient()
	if err != nil {
		return nil, err
	}

	listers := &ntoclient.Listers{}
	clients := &ntoclient.Clients{}
	controller := &Controller{
		kubeconfig:   kubeconfig,
		kubeclient:   kubeclient,
		listers:      listers,
		clients:      clients,
		tunedExit:    make(chan bool, 1),
		stopCh:       stopCh,
		changeCh:     make(chan bool, 1),
		changeChRet:  make(chan bool, 1),
		tunedTicker:  time.NewTicker(math.MaxInt64),
		tunedTimeout: tunedInitialTimeout,
	}
	controller.tunedTicker.Stop() // The ticker will be started/reset when TuneD starts.

	return controller, nil
}

// eventProcessorKube is a long-running method that will continually
// read and process messages on the wqKube workqueue.
func (c *Controller) eventProcessorKube() {
	for {
		// Wait until there is a new item in the working queue.
		obj, shutdown := c.wqKube.Get()
		if shutdown {
			return
		}

		klog.V(2).Infof("got event from workqueue")
		func() {
			defer c.wqKube.Done(obj)
			var workqueueKey wqKey
			var ok bool

			if workqueueKey, ok = obj.(wqKey); !ok {
				c.wqKube.Forget(obj)
				klog.Errorf("expected wqKey in workqueue but got %#v", obj)
				return
			}

			if err := c.sync(workqueueKey); err != nil {
				requeued := c.wqKube.NumRequeues(workqueueKey)
				// Limit retries to maxRetries.  After that, stop trying.
				if requeued < maxRetries {
					klog.Errorf("unable to sync(%s/%s) requeued (%d): %v", workqueueKey.kind, workqueueKey.name, requeued, err)

					// Re-enqueue the workqueueKey.  Based on the rate limiter on the queue
					// and the re-enqueue history, the workqueueKey will be processed later again.
					c.wqKube.AddRateLimited(workqueueKey)
					return
				}
				klog.Errorf("unable to sync(%s/%s) reached max retries (%d): %v", workqueueKey.kind, workqueueKey.name, maxRetries, err)
				// Dropping the item after maxRetries unsuccessful retries.
				c.wqKube.Forget(obj)
				return
			}
			klog.V(1).Infof("event from workqueue (%s/%s) successfully processed", workqueueKey.kind, workqueueKey.name)
			// Successful processing.
			c.wqKube.Forget(obj)
		}()
	}
}

func (c *Controller) sync(key wqKey) error {
	switch {
	case key.kind == wqKindTuned:
		if key.name != tunedv1.TunedRenderedResourceName {
			return nil
		}
		klog.V(2).Infof("sync(): Tuned %s", key.name)

		tuned, err := c.listers.TunedResources.Get(key.name)
		if err != nil {
			return fmt.Errorf("failed to get Tuned %s: %v", key.name, err)
		}

		change, err := profilesSync(tuned.Spec.Profile, c.daemon.recommendedProfile)
		if err != nil {
			return err
		}
		c.change.rendered = change
		// Notify the event processor that the Tuned k8s object containing TuneD profiles changed.
		c.wqTuneD.Add(wqKey{kind: wqKindDaemon})

		return nil

	case key.kind == wqKindProfile:
		if key.name != getNodeName() {
			return nil
		}
		klog.V(2).Infof("sync(): Profile %s", key.name)

		profile, err := c.listers.TunedProfiles.Get(getNodeName())
		if err != nil {
			return fmt.Errorf("failed to get Profile %s: %v", key.name, err)
		}

		err = providerExtract(profile.Spec.Config.ProviderName)
		if err != nil {
			return err
		}

		c.daemon.recommendedProfile = profile.Spec.Config.TunedProfile
		err = tunedRecommendFileWrite(c.daemon.recommendedProfile)
		if err != nil {
			return err
		}
		c.change.profile = true

		if c.daemon.debug != profile.Spec.Config.Debug {
			c.change.daemon = true // A complete restart of the TuneD daemon is needed due to a debugging request switched on or off.
			c.daemon.debug = profile.Spec.Config.Debug
		}
		if profile.Spec.Config.TuneDConfig.ReapplySysctl != nil {
			reapplySysctl := c.tunedMainCfg.Section("").Key("reapply_sysctl").MustBool()
			if *profile.Spec.Config.TuneDConfig.ReapplySysctl != reapplySysctl {
				//nolint:errcheck
				iniCfgSetKey(c.tunedMainCfg, "reapply_sysctl", !reapplySysctl)
				err = iniFileSave(tunedProfilesDirCustom+"/"+tunedMainConfFile, c.tunedMainCfg)
				if err != nil {
					return fmt.Errorf("failed to write global TuneD configuration file: %v", err)
				}
				c.change.daemon = true // A complete restart of the TuneD daemon is needed due to configuration change in tunedMainConfFile.
			}
		}
		// Notify the event processor that the Profile k8s object containing information about which TuneD profile to apply changed.
		c.wqTuneD.Add(wqKey{kind: wqKindDaemon})

		return nil

	default:
	}

	return nil
}

func disableSystemTuned() {
	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)
	klog.Infof("disabling system tuned...")
	cmd := exec.Command("/usr/bin/systemctl", "disable", "tuned", "--now")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		klog.V(1).Infof("failed to disable system tuned: %v: %s", err, stderr.String()) // do not use log.Printf(), tuned has its own timestamping
	}
}

func profilesEqual(profileFile string, profileData string) bool {
	content, err := os.ReadFile(profileFile)
	if err != nil {
		content = []byte{}
	}

	return profileData == string(content)
}

// profilesExtract extracts TuneD daemon profiles to the daemon configuration directory.
// Returns:
//   - True if the data in the to-be-extracted recommended profile or the profiles being
//     included from the current recommended profile have changed.
//   - A map with successfully extracted TuneD profile names.
//   - A map with names of TuneD profiles the current TuneD recommended profile depends on.
//   - Error if any or nil.
func profilesExtract(profiles []tunedv1.TunedProfile, recommendedProfile string) (bool, map[string]bool, map[string]bool, error) {
	var (
		change bool
	)
	klog.Infof("extracting TuneD profiles")

	// Get a list of TuneD profiles names the recommended profile depends on.
	recommendedProfileDeps := profileDepends(recommendedProfile)
	// Add the recommended profile itself.
	recommendedProfileDeps[recommendedProfile] = true
	extracted := map[string]bool{} // TuneD profile names present in TuneD CR and successfully extracted to /etc/tuned/<profile>/

	for index, profile := range profiles {
		if profile.Name == nil {
			klog.Warningf("profilesExtract(): profile name missing for Profile %v", index)
			continue
		}
		if profile.Data == nil {
			klog.Warningf("profilesExtract(): profile data missing for Profile %v", index)
			continue
		}
		profileDir := fmt.Sprintf("%s/%s", tunedProfilesDirCustom, *profile.Name)
		profileFile := fmt.Sprintf("%s/%s", profileDir, tunedConfFile)

		if err := util.Mkdir(profileDir); err != nil {
			return change, extracted, recommendedProfileDeps, fmt.Errorf("failed to create TuneD profile directory %q: %v", profileDir, err)
		}

		if recommendedProfileDeps[*profile.Name] {
			// Recommended profile (dependency) name matches profile name of the profile
			// currently being extracted, compare their content.
			var un string
			change = change || !profilesEqual(profileFile, *profile.Data)
			if !change {
				un = "un"
			}
			klog.Infof("recommended TuneD profile %s content %schanged [%s]", recommendedProfile, un, *profile.Name)
		}

		f, err := os.Create(profileFile)
		if err != nil {
			return change, extracted, recommendedProfileDeps, fmt.Errorf("failed to create TuneD profile file %q: %v", profileFile, err)
		}
		defer f.Close()
		if _, err = f.WriteString(*profile.Data); err != nil {
			return change, extracted, recommendedProfileDeps, fmt.Errorf("failed to write TuneD profile file %q: %v", profileFile, err)
		}
		extracted[*profile.Name] = true
	}

	return change, extracted, recommendedProfileDeps, nil
}

// profilesSync extracts TuneD daemon profiles to the daemon configuration directory
// and removes any TuneD profiles from /etc/tuned/<profile>/ once the same TuneD
// <profile> is no longer defined in the 'profiles' slice.
// Returns:
//   - True if the data in the to-be-extracted recommended profile or the profiles being
//     included from the current recommended profile have changed.
//   - Error if any or nil.
func profilesSync(profiles []tunedv1.TunedProfile, recommendedProfile string) (bool, error) {
	change, extractedNew, recommendedProfileDeps, err := profilesExtract(profiles, recommendedProfile)
	if err != nil {
		return change, err
	}

	// Deal with TuneD profiles absent from Tuned CRs, but still present in /etc/tuned/<profile>/ the recommended profile depends on.
	for profile := range recommendedProfileDeps {
		if len(profile) == 0 {
			continue
		}

		if !extractedNew[profile] {
			// TuneD profile does not exist in the Tuned CR, but the recommended profile depends on it.
			profileDir := fmt.Sprintf("%s/%s", tunedProfilesDirCustom, profile)
			if _, err := os.Stat(profileDir); err == nil {
				// We have a stale TuneD profile directory in /etc/tuned/<profile>/
				// Remove it.
				err := os.RemoveAll(profileDir)
				if err != nil {
					return change, fmt.Errorf("failed to remove %q: %v", profileDir, err)
				}
				change = true
				klog.Infof("removed TuneD profile %q", profileDir)
			}
		}
	}

	return change, nil
}

// providerExtract extracts Cloud Provider name into openshiftTunedProvider file.
func providerExtract(provider string) error {
	klog.Infof("extracting cloud provider name to %v", openshiftTunedProvider)

	f, err := os.Create(openshiftTunedProvider)
	if err != nil {
		return fmt.Errorf("failed to create cloud provider name file %q: %v", openshiftTunedProvider, err)
	}
	defer f.Close()
	if _, err = f.WriteString(provider); err != nil {
		return fmt.Errorf("failed to write cloud provider name file %q: %v", openshiftTunedProvider, err)
	}

	return nil
}

func openshiftTunedPidFileWrite() error {
	if err := util.Mkdir(openshiftTunedRunDir); err != nil {
		return fmt.Errorf("failed to create %s run directory %q: %v", programName, openshiftTunedRunDir, err)
	}
	f, err := os.Create(openshiftTunedPidFile)
	if err != nil {
		return fmt.Errorf("failed to create %s pid file %q: %v", programName, openshiftTunedPidFile, err)
	}
	defer f.Close()
	if _, err = f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		return fmt.Errorf("failed to write %s pid file %q: %v", programName, openshiftTunedPidFile, err)
	}
	return nil
}

func tunedRecommendFileWrite(profileName string) error {
	klog.V(2).Infof("tunedRecommendFileWrite(): %s", profileName)
	if err := util.Mkdir(tunedRecommendDir); err != nil {
		return fmt.Errorf("failed to create directory %q: %v", tunedRecommendDir, err)
	}
	f, err := os.Create(tunedRecommendFile)
	if err != nil {
		return fmt.Errorf("failed to create file %q: %v", tunedRecommendFile, err)
	}
	defer f.Close()
	if _, err = f.WriteString(fmt.Sprintf("[%s]\n", profileName)); err != nil {
		return fmt.Errorf("failed to write file %q: %v", tunedRecommendFile, err)
	}
	klog.Infof("written %q to set TuneD profile %s", tunedRecommendFile, profileName)
	return nil
}

// isSysctlOverride returns name of a host-level sysctl that overrides TuneD-level sysctl,
// or an empty string.
func overridenSysctl(data string) string {
	// Example log line to parse:
	// 2023-02-10 12:57:13,201 INFO     tuned.plugins.plugin_sysctl: Overriding sysctl parameter 'fs.inotify.max_user_instances' from '4096' to '8192'
	var (
		overridenSysctl = ""
	)
	const (
		key = "tuned.plugins.plugin_sysctl: Overriding sysctl parameter '"
	)
	i := strings.Index(data, key)
	if i == -1 {
		return ""
	}
	i += len(key)
	slice := strings.Split(data[i:], "'")
	if slice[2] != slice[4] {
		overridenSysctl = slice[0]
	}

	return overridenSysctl
}

func (c *Controller) tunedCreateCmd() *exec.Cmd {
	args := []string{"--no-dbus"}
	if c.daemon.debug {
		args = append(args, "--debug")
	}
	return exec.Command("/usr/sbin/tuned", args...)
}

func (c *Controller) tunedRun() {
	klog.Infof("starting tuned...")

	defer func() {
		close(c.tunedExit)
	}()

	cmdReader, err := c.tunedCmd.StderrPipe()
	if err != nil {
		klog.Errorf("error creating StderrPipe for tuned: %v", err)
		return
	}

	scanner := bufio.NewScanner(cmdReader)
	go func() {
		for scanner.Scan() {
			l := scanner.Text()

			fmt.Printf("%s\n", l)

			if c.daemon.stopping {
				// We have decided to stop TuneD.  Apart from showing the logs it is
				// now unnecessary/undesirable to perform any of the following actions.
				// The undesirability comes from extra processing which will come if
				// TuneD manages to "get unstuck" during this phase before it receives
				// SIGKILL (note the time window between SIGTERM/SIGKILL).
				continue
			}

			profileApplied := strings.Contains(l, " tuned.daemon.daemon: static tuning from profile ") && strings.Contains(l, " applied")
			reloadFailed := strings.Contains(l, " tuned.daemon.controller: Failed to reload TuneD: ")

			if profileApplied {
				c.daemon.status |= scApplied
			}

			strIndex := strings.Index(l, " WARNING ")
			if strIndex >= 0 {
				c.daemon.status |= scWarn
				prevError := ((c.daemon.status & scError) != 0)
				if !prevError { // don't overwrite an error message
					c.daemon.stderr = l[strIndex:] // trim timestamp from log
				}
			}

			strIndex = strings.Index(l, " ERROR ")
			if strIndex >= 0 {
				c.daemon.status |= scError
				c.daemon.stderr = l[strIndex:] // trim timestamp from log
			}

			sysctl := overridenSysctl(l)
			if sysctl != "" {
				c.daemon.status |= scSysctlOverride
				c.daemon.stderr = sysctl
			}

			if c.daemon.reloading {
				c.daemon.reloading = !profileApplied && !reloadFailed
				c.daemon.reloaded = !c.daemon.reloading
				if c.daemon.reloaded {
					klog.V(2).Infof("profile applied or reload failed, stopping the TuneD watcher")
					c.tunedTimeout = tunedInitialTimeout // initialize the timeout
					c.daemon.status &= ^scTimeout        // clear the scTimeout status bit
					c.tunedTicker.Stop()                 // profile applied or reload failed, stop the TuneD watcher

					// Notify the event processor that the TuneD daemon finished reloading.
					c.wqTuneD.Add(wqKey{kind: wqKindDaemon})
				}
			}
		}
	}()

	c.daemon.reloading = true
	// Clear the set out of which Profile status conditions are created. Keep timeout condition if already set.
	c.daemon.status &= scTimeout
	c.daemon.stderr = ""
	if err = c.tunedCmd.Start(); err != nil {
		klog.Errorf("error starting tuned: %v", err)
		return
	}

	if err = c.tunedCmd.Wait(); err != nil {
		// The command exited with non 0 exit status, e.g. terminated by a signal.
		klog.Errorf("error waiting for tuned: %v", err)
		return
	}
}

// tunedStop tries to gracefully stop the TuneD daemon process by sending it SIGTERM.
// If the TuneD daemon does not respond by terminating within tunedGracefulExitWait
// duration, SIGKILL is sent.  This method returns an indication whether the TuneD
// daemon exitted gracefully (true) or SIGKILL had to be sent (false).
func (c *Controller) tunedStop() (bool, error) {
	c.daemon.stopping = true
	defer func() {
		c.daemon.stopping = false
	}()

	if c.tunedCmd == nil {
		// Looks like there has been a termination signal prior to starting tuned.
		return false, nil
	}
	if c.tunedCmd.Process != nil {
		// The TuneD daemon rolls back the current profile and should terminate on SIGTERM.
		klog.V(1).Infof("sending SIGTERM to PID %d", c.tunedCmd.Process.Pid)
		//nolint:errcheck
		c.tunedCmd.Process.Signal(syscall.SIGTERM)
	} else {
		// This should never happen!
		return false, fmt.Errorf("cannot find the TuneD process!")
	}
	// Wait for TuneD process to stop -- this will enable node-level tuning rollback.
	select {
	case <-c.tunedExit:
	case <-time.After(tunedGracefulExitWait):
		// It looks like the TuneD daemon refuses to terminate gracefully on SIGTERM
		// within tunedGracefulExitWait.
		klog.V(1).Infof("sending SIGKILL to PID %d", c.tunedCmd.Process.Pid)
		//nolint:errcheck
		c.tunedCmd.Process.Signal(syscall.SIGKILL)
		<-c.tunedExit
		return false, nil
	}
	klog.V(1).Infof("TuneD process terminated gracefully")

	return true, nil
}

func (c *Controller) tunedReload(timeoutInitiated bool) error {
	c.daemon.reloading = true
	c.daemon.status = 0 // clear the set out of which Profile status conditions are created
	c.daemon.stderr = ""

	tunedTimeout := time.Second * time.Duration(c.tunedTimeout)
	if c.tunedTicker == nil {
		// This should never happen as the ticker is initialized at controller creation time.
		c.tunedTicker = time.NewTicker(tunedTimeout)
	} else {
		c.tunedTicker.Reset(tunedTimeout)
	}
	if timeoutInitiated {
		c.daemon.status = scTimeout // timeout waiting for the daemon should be reported to Profile status
		c.tunedTimeout *= 2
	}

	if c.tunedCmd == nil {
		// TuneD hasn't been started by openshift-tuned, start it.
		c.tunedCmd = c.tunedCreateCmd()
		go c.tunedRun()
		return nil
	}

	klog.Infof("reloading tuned...")

	if c.tunedCmd.Process != nil {
		klog.Infof("sending HUP to PID %d", c.tunedCmd.Process.Pid)
		err := c.tunedCmd.Process.Signal(syscall.SIGHUP)
		if err != nil {
			return fmt.Errorf("error sending SIGHUP to PID %d: %v\n", c.tunedCmd.Process.Pid, err)
		}
	} else {
		// This should never happen!
		return fmt.Errorf("cannot find the TuneD process!")
	}

	return nil
}

// tunedRestart restarts the TuneD daemon.  The "stop" part is synchronous
// to ensure proper termination of TuneD, the "start" part is asynchronous.
func (c *Controller) tunedRestart(timeoutInitiated bool) (err error) {
	if _, err = c.tunedStop(); err != nil {
		return err
	}
	c.tunedCmd = nil                 // Cmd.Start() cannot be used more than once
	c.tunedExit = make(chan bool, 1) // Once tunedStop() terminates, the tunedExit channel is closed!

	if err = c.tunedReload(timeoutInitiated); err != nil {
		return err
	}
	return nil
}

// getActiveProfile returns active profile currently in use by the TuneD daemon.
// On error, an empty string is returned.
func getActiveProfile() (string, error) {
	var responseString = ""

	f, err := os.Open(tunedActiveProfileFile)
	if err != nil {
		return "", fmt.Errorf("error opening Tuned active profile file %s: %v", tunedActiveProfileFile, err)
	}
	defer f.Close()

	var scanner = bufio.NewScanner(f)
	for scanner.Scan() {
		responseString = strings.TrimSpace(scanner.Text())
	}

	return responseString, nil
}

func getBootcmdline() (string, error) {
	var responseString = ""

	f, err := os.Open(tunedBootcmdlineFile)
	if err != nil {
		return "", fmt.Errorf("error opening Tuned bootcmdline file %s: %v", tunedBootcmdlineFile, err)
	}
	defer f.Close()

	var scanner = bufio.NewScanner(f)
	for scanner.Scan() {
		s := strings.TrimSpace(scanner.Text())
		if i := strings.Index(s, "TUNED_BOOT_CMDLINE="); i == 0 {
			responseString = s[len("TUNED_BOOT_CMDLINE="):]

			if len(responseString) > 0 && (responseString[0] == '"' || responseString[0] == '\'') {
				responseString = responseString[1 : len(responseString)-1]
			}
			// Don't break here to behave more like shell evaluation.
		}
	}

	return responseString, nil
}

// Method changeSyncer performs k8s Profile object updates and TuneD daemon
// reloads as needed.  Returns indication whether the change was successfully
// synced and an error.  Only critical errors are returned, as non-nil errors
// will cause restart of the main control loop -- the changeWatcher() method.
func (c *Controller) changeSyncer() (synced bool, err error) {
	var reload bool

	if c.daemon.reloading {
		// This should not be necessary, but keep this here as a reminder.
		return false, fmt.Errorf("changeSyncer(): called while the TuneD daemon was reloading")
	}

	if c.change.bootcmdline || c.daemon.reloaded {
		// One or both of the following happened:
		// 1) tunedBootcmdlineFile changed on the filesystem.  This is very likely the result of
		//    applying a TuneD profile by the TuneD daemon.  Make sure the node Profile k8s object
		//    is in sync with tunedBootcmdlineFile so the operator can take an appropriate action.
		// 2) TuneD daemon was reloaded.  Make sure the node Profile k8s object is in sync with
		//    the active profile, e.g. the Profile indicates the presence of the stall daemon on
		//    the host if requested by the current active profile.
		if err = c.updateTunedProfile(); err != nil {
			klog.Error(err.Error())
			return false, nil // retry later
		} else {
			// The node Profile k8s object was updated successfully.  Clear the flags indicating
			// a check for syncing the object is needed.
			c.change.bootcmdline = false
			c.daemon.reloaded = false
		}
	}

	// Check whether reload of the TuneD daemon is really necessary due to a Profile change.
	if c.change.profile {
		// The node Profile k8s object changed.
		var activeProfile string
		if activeProfile, err = getActiveProfile(); err != nil {
			return false, err
		}
		if (c.daemon.status & scApplied) == 0 {
			if len(activeProfile) > 0 {
				// activeProfile == "" means we have not started TuneD daemon yet; do not log that case
				klog.Infof("re-applying profile (%s) as the previous application did not complete", activeProfile)
			}
			reload = true
		} else if (c.daemon.status & scError) != 0 {
			klog.Infof("re-applying profile (%s) as the previous application ended with error(s)", activeProfile)
			reload = true
		} else if activeProfile != c.daemon.recommendedProfile {
			klog.Infof("active profile (%s) != recommended profile (%s)", activeProfile, c.daemon.recommendedProfile)
			reload = true
			c.daemon.status = scUnknown
		} else {
			klog.Infof("active and recommended profile (%s) match; profile change will not trigger profile reload", activeProfile)
			// We do not need to reload the TuneD daemon, however, someone may have tampered with the k8s Profile for this node.
			// Make sure it is up-to-date.
			if err = c.updateTunedProfile(); err != nil {
				klog.Error(err.Error())
				return false, nil // retry later
			}
		}
		c.change.profile = false
	}
	if c.change.rendered {
		// The "rendered" Tuned k8s object changed.
		c.change.rendered = false
		reload = true
		c.daemon.status = scUnknown
	}

	if c.change.daemon {
		// Complete restart of the TuneD daemon needed (e.g. using --debug option).
		c.change.daemon = false
		c.daemon.status = scUnknown
		err = c.tunedRestart(false)
		return err == nil, err
	}

	if reload {
		err = c.tunedReload(false)
	}
	return err == nil, err
}

// eventProcessorTuneD is a long-running method that will continually
// read and process messages on the wqTuneD workqueue.
func (c *Controller) eventProcessorTuneD() {
	for {
		// Wait until there is a new item in the working queue.
		obj, shutdown := c.wqTuneD.Get()
		if shutdown {
			return
		}

		klog.V(2).Infof("got event from workqueue")
		func() {
			defer c.wqTuneD.Done(obj)
			var workqueueKey wqKey
			var ok bool

			if workqueueKey, ok = obj.(wqKey); !ok {
				c.wqTuneD.Forget(obj)
				klog.Errorf("expected wqKey in workqueue but got %#v", obj)
				return
			}

			c.changeCh <- true
			eventProcessed := <-c.changeChRet
			if !eventProcessed {
				requeued := c.wqTuneD.NumRequeues(workqueueKey)
				// Limit retries to maxRetries.  After that, stop trying.
				if requeued < maxRetries {
					klog.Errorf("unable to sync(%s/%s) requeued (%d)", workqueueKey.kind, workqueueKey.name, requeued)

					// Re-enqueue the workqueueKey.  Based on the rate limiter on the queue
					// and the re-enqueue history, the workqueueKey will be processed later again.
					c.wqTuneD.AddRateLimited(workqueueKey)
					return
				}
				klog.Errorf("unable to sync(%s/%s) reached max retries (%d)", workqueueKey.kind, workqueueKey.name, maxRetries)
				// Dropping the item after maxRetries unsuccessful retries.
				c.wqTuneD.Forget(obj)
				return
			}
			klog.V(1).Infof("event from workqueue (%s/%s) successfully processed", workqueueKey.kind, workqueueKey.name)
			// Successful processing.
			c.wqTuneD.Forget(obj)
		}()
	}
}

func getNodeName() string {
	name := os.Getenv("OCP_NODE_NAME")
	if len(name) == 0 {
		// Something is seriously wrong, OCP_NODE_NAME must be defined via Tuned DaemonSet.
		panic("OCP_NODE_NAME unset or empty")
	}
	return name
}

func (c *Controller) getNodeForProfile(nodeName string) (*corev1.Node, error) {
	node, err := c.kubeclient.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Node %s: %v", nodeName, err)
	}
	return node, nil
}

func (c *Controller) updateNodeAnnotations(node *corev1.Node, annotations map[string]string) (err error) {
	var (
		change bool
	)
	node = node.DeepCopy() // never update the objects from cache

	if node.ObjectMeta.Annotations == nil {
		node.ObjectMeta.Annotations = map[string]string{}
	}

	for k, v := range annotations {
		change = change || node.ObjectMeta.Annotations[k] != v
		node.ObjectMeta.Annotations[k] = v
	}

	if !change {
		// No Node annotations changed, no need to update
		return nil
	}

	// See OCPBUGS-17585: Instead of carrying information that could affect other cluster nodes in
	// Profiles's status, use the kubelet's credentials to write to the Node's resource.
	_, err = c.kubeclient.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Node %s: %v", node.ObjectMeta.Name, err)
	}

	return nil
}

// Method updateTunedProfile updates a Tuned Profile with information to report back
// to the operator.  Note this method must be called only when the TuneD daemon is
// not reloading.
func (c *Controller) updateTunedProfile() (err error) {
	var (
		bootcmdline string
	)

	if bootcmdline, err = getBootcmdline(); err != nil {
		// This should never happen unless something is seriously wrong (e.g. TuneD
		// daemon no longer uses tunedBootcmdlineFile).  Do not continue.
		return fmt.Errorf("unable to get kernel command-line parameters: %v", err)
	}

	profileName := getNodeName()
	profile, err := c.listers.TunedProfiles.Get(profileName)
	if err != nil {
		return fmt.Errorf("failed to get Profile %s: %v", profileName, err)
	}

	activeProfile, err := getActiveProfile()
	if err != nil {
		return err
	}

	node, err := c.getNodeForProfile(getNodeName())
	if err != nil {
		return err
	}

	if node.ObjectMeta.Annotations == nil {
		node.ObjectMeta.Annotations = map[string]string{}
	}

	statusConditions := computeStatusConditions(c.daemon.status, c.daemon.stderr, profile.Status.Conditions)
	bootcmdlineAnnotVal, bootcmdlineAnnotSet := node.ObjectMeta.Annotations[tunedv1.TunedBootcmdlineAnnotationKey]

	if bootcmdlineAnnotSet && bootcmdlineAnnotVal == bootcmdline &&
		profile.Status.TunedProfile == activeProfile &&
		conditionsEqual(profile.Status.Conditions, statusConditions) {
		// Do not update node Profile unnecessarily (e.g. bootcmdline did not change).
		// This will save operator CPU cycles trying to reconcile objects that do not
		// need reconciling.
		klog.V(2).Infof("updateTunedProfile(): no need to update Profile %s", profile.Name)
		return nil
	}

	annotations := map[string]string{tunedv1.TunedBootcmdlineAnnotationKey: bootcmdline}
	err = c.updateNodeAnnotations(node, annotations)
	if err != nil {
		return err
	}

	profile = profile.DeepCopy() // never update the objects from cache

	profile.Status.TunedProfile = activeProfile
	profile.Status.Conditions = statusConditions
	_, err = c.clients.Tuned.TunedV1().Profiles(operandNamespace).UpdateStatus(context.TODO(), profile, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Profile %s status: %v", profile.Name, err)
	}
	klog.Infof("updated Profile %s bootcmdline: %s", profile.Name, bootcmdline)

	return nil
}

func (c *Controller) informerEventHandler(workqueueKey wqKey) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(o interface{}) {
			accessor, err := kmeta.Accessor(o)
			if err != nil {
				klog.Errorf("unable to get accessor for added object: %s", err)
				return
			}

			workqueueKey.name = accessor.GetName()
			if workqueueKey.kind == wqKindProfile && workqueueKey.name == getNodeName() {
				// When moving this code elsewhere, consider whether it is desirable
				// to disable system tuned on nodes that should not be managed by
				// openshift-tuned.
				disableSystemTuned()
			}

			klog.V(2).Infof("add event to workqueue due to %s (add)", util.ObjectInfo(o))
			c.wqKube.Add(wqKey{kind: workqueueKey.kind, name: workqueueKey.name})
		},
		UpdateFunc: func(o, n interface{}) {
			newAccessor, err := kmeta.Accessor(n)
			if err != nil {
				klog.Errorf("unable to get accessor for new object: %s", err)
				return
			}
			oldAccessor, err := kmeta.Accessor(o)
			if err != nil {
				klog.Errorf("unable to get accessor for old object: %s", err)
				return
			}
			if newAccessor.GetResourceVersion() == oldAccessor.GetResourceVersion() {
				// Periodic resync will send update events for all known resources.
				// Two different versions of the same resource will always have different RVs.
				return
			}
			klog.V(2).Infof("add event to workqueue due to %s (update)", util.ObjectInfo(n))
			c.wqKube.Add(wqKey{kind: workqueueKey.kind, name: newAccessor.GetName()})
		},
		DeleteFunc: func(o interface{}) {
			object, ok := o.(metav1.Object)
			if !ok {
				tombstone, ok := o.(cache.DeletedFinalStateUnknown)
				if !ok {
					klog.Errorf("error decoding object, invalid type")
					return
				}
				object, ok = tombstone.Obj.(metav1.Object)
				if !ok {
					klog.Errorf("error decoding object tombstone, invalid type")
					return
				}
				klog.V(4).Infof("recovered deleted object %s from tombstone", object.GetName())
			}
			klog.V(2).Infof("add event to workqueue due to %s (delete)", util.ObjectInfo(object))
			c.wqKube.Add(wqKey{kind: workqueueKey.kind, name: object.GetName()})
		},
	}
}

// The changeWatcher method is the main control loop watching for changes to be applied
// and supervising the TuneD daemon.  On successful (error == nil) exit, no attempt at
// reentering this control loop should be made as it is an indication of an intentional
// exit on request.
func (c *Controller) changeWatcher() (err error) {
	c.tunedMainCfg, err = iniFileLoad(tunedProfilesDirCustom + "/" + tunedMainConfFile)
	if err != nil {
		return fmt.Errorf("failed to load global TuneD configuration file: %v", err)
	}

	// Use less aggressive per-item only exponential rate limiting for both wqKube and wqTuneD.
	// Start retrying at 100ms with a maximum of 1800s.
	c.wqKube = workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(100*time.Millisecond, 1800*time.Second))
	c.wqTuneD = workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(100*time.Millisecond, 1800*time.Second))

	c.clients.Tuned, err = tunedset.NewForConfig(c.kubeconfig)
	if err != nil {
		return err
	}

	tunedInformerFactory := tunedinformers.NewSharedInformerFactoryWithOptions(
		c.clients.Tuned,
		ntoconfig.ResyncPeriod(),
		tunedinformers.WithNamespace(operandNamespace))

	trInformer := tunedInformerFactory.Tuned().V1().Tuneds()
	c.listers.TunedResources = trInformer.Lister().Tuneds(operandNamespace)
	//nolint:errcheck
	trInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindTuned}))

	tpInformer := tunedInformerFactory.Tuned().V1().Profiles()
	c.listers.TunedProfiles = tpInformer.Lister().Profiles(operandNamespace)
	//nolint:errcheck
	tpInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindProfile}))

	tunedInformerFactory.Start(c.stopCh) // Tuned/Profile

	// Wait for the caches to be synced before starting worker(s).
	klog.V(1).Info("waiting for informer caches to sync")
	ok := cache.WaitForCacheSync(c.stopCh,
		trInformer.Informer().HasSynced,
		tpInformer.Informer().HasSynced,
	)
	if !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.V(1).Info("starting events processors")
	go wait.Until(c.eventProcessorKube, time.Second, c.stopCh)
	defer c.wqKube.ShutDown()
	go wait.Until(c.eventProcessorTuneD, time.Second, c.stopCh)
	defer c.wqTuneD.ShutDown()
	klog.Info("started events processors")

	// Watch for filesystem changes on the tunedBootcmdlineFile file.
	wFs, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create filesystem watcher: %v", err)
	}
	defer wFs.Close()

	// Register fsnotify watchers.
	for _, element := range []string{tunedBootcmdlineFile} {
		err = wFs.Add(element)
		if err != nil {
			return fmt.Errorf("failed to start watching %q: %v", element, err)
		}
	}

	klog.Info("started controller")
	for {
		select {
		case <-c.stopCh:
			klog.Infof("termination signal received, stop")

			return nil

		case <-c.tunedExit:
			c.tunedCmd = nil // Cmd.Start() cannot be used more than once
			klog.Infof("TuneD process exitted...")

			// Do not be too aggressive about keeping the TuneD daemon around.
			// TuneD daemon might have exitted after receiving SIGTERM during
			// system reboot/shutdown.
			return nil

		case fsEvent := <-wFs.Events:
			klog.V(2).Infof("fsEvent")
			if fsEvent.Op&fsnotify.Write == fsnotify.Write {
				klog.V(1).Infof("write event on: %s", fsEvent.Name)
				c.change.bootcmdline = true
				// Notify the event processor that the TuneD daemon calculated new kernel command-line parameters.
				c.wqTuneD.Add(wqKey{kind: wqKindDaemon})
			}

		case err := <-wFs.Errors:
			return fmt.Errorf("error watching filesystem: %v", err)

		case <-c.tunedTicker.C:
			klog.Errorf("timeout (%d) to apply TuneD profile; restarting TuneD daemon", c.tunedTimeout)
			err := c.tunedRestart(true)
			if err != nil {
				return err
			}
			// TuneD profile application is failing, make this visible in "oc get profile" output.
			if err = c.updateTunedProfile(); err != nil {
				klog.Error(err.Error())
			}

		case <-c.changeCh:
			var synced bool
			klog.V(2).Infof("changeCh")
			if c.tunedTimeout > tunedInitialTimeout {
				// TuneD is "degraded" as the previous profile application did not succeed in
				// tunedInitialTimeout [s].  There has been a change we must act upon though
				// fairly quickly.
				c.tunedTimeout = tunedInitialTimeout
				klog.Infof("previous application of TuneD profile failed; change detected, scheduling full restart in 1s")
				c.tunedTicker.Reset(time.Second * time.Duration(1))
				c.changeChRet <- true
				continue
			}

			if c.daemon.reloading {
				// Do not reload the TuneD daemon unless it finished with the previous reload.
				c.changeChRet <- false
				continue
			}

			synced, err := c.changeSyncer()
			if err != nil {
				return err
			}

			c.changeChRet <- synced
		}
	}
}

func retryLoop(c *Controller) (err error) {
	const (
		errsMax        = 5  // the maximum number of consecutive errors within errsMaxWithinSeconds
		sleepRetryInit = 10 // the initial retry period [s]
	)
	var (
		errs       int
		sleepRetry int64 = sleepRetryInit
		// sum of the series: S_n = x(1)*(q^n-1)/(q-1) + add 60s for each changeWatcher() call
		errsMaxWithinSeconds int64 = (sleepRetry*int64(math.Pow(2, errsMax)) - sleepRetry) + errsMax*60
	)

	defer func() {
		if c.tunedCmd == nil {
			return
		}
		if c.tunedCmd.Process != nil {
			if _, err := c.tunedStop(); err != nil {
				klog.Errorf("%s", err.Error())
			}
		} else {
			// This should never happen!
			klog.Errorf("cannot find the TuneD process!")
		}
	}()

	errsTimeStart := time.Now().Unix()
	for {
		err = c.changeWatcher()
		if err == nil {
			return nil
		}

		select {
		case <-c.stopCh:
			return err
		default:
		}

		klog.Errorf("%s", err.Error())
		sleepRetry *= 2
		klog.Infof("increased retry period to %d", sleepRetry)
		if errs++; errs >= errsMax {
			now := time.Now().Unix()
			if (now - errsTimeStart) <= errsMaxWithinSeconds {
				klog.Errorf("seen %d errors in %d seconds (limit was %d), terminating...", errs, now-errsTimeStart, errsMaxWithinSeconds)
				return err
			}
			errs = 0
			sleepRetry = sleepRetryInit
			errsTimeStart = time.Now().Unix()
			klog.Infof("initialized retry period to %d", sleepRetry)
		}

		select {
		case <-c.stopCh:
			return nil
		case <-time.After(time.Second * time.Duration(sleepRetry)):
			continue
		}
	}
}

func Run(stopCh <-chan struct{}, boolVersion *bool, version string) {
	klog.Infof("starting %s %s", programName, version)
	parseCmdOpts()

	if *boolVersion {
		fmt.Fprintf(os.Stderr, "%s %s\n", programName, version)
		os.Exit(0)
	}

	err := openshiftTunedPidFileWrite()
	if err != nil {
		// openshift-tuned PID file is not really used by anything, remove it in the future?
		panic(err.Error())
	}

	c, err := newController(stopCh)
	if err != nil {
		// This looks really bad, there was an error creating the Controller.
		panic(err.Error())
	}

	err = retryLoop(c)
	if err != nil {
		panic(err.Error())
	}
}
