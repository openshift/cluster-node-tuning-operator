package tuned

import (
	"bufio"   // scanner
	"bytes"   // bytes.Buffer
	"context" // context.TODO()
	"fmt"     // Printf()
	"math"    // math.Pow()
	"os"      // os.Exit(), os.Stderr, ...
	"os/exec" // os.Exec()
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
	scReloading // reloading is true during the TuneD daemon reload.
	scUnknown
)

const (
	// Constants used for controlling TuneD;
	// they will be set to 2^0, 2^1, 2^2, ..., 2^n
	ctrlReload Bits = 1 << iota
	// Did the command-line parameters to run the TuneD daemon or tuned-main.conf change?
	// In other words, is a complete restart of the TuneD daemon needed?
	ctrlRestart
	ctrlDebug // Debugging can be turned on only from the command-line.
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
	tunedGracefulExitWait  = time.Second * time.Duration(10)
	openshiftTunedHome     = "/var/lib/ocp-tuned"
	openshiftTunedRunDir   = "/run/" + programName
	openshiftTunedProvider = openshiftTunedHome + "/provider"
	// With the less aggressive rate limiter, retries will happen at 100ms*2^(retry_n-1):
	// 100ms, 200ms, 400ms, 800ms, 1.6s, 3.2s, 6.4s, 12.8s, 25.6s, 51.2s, 102.4s, 3.4m, 6.8m, 13.7m, 27.3m
	maxRetries = 15
	// workqueue related constants
	wqKindDaemon  = "daemon"
	wqKindTuned   = "tuned"
	wqKindProfile = "profile"
)

// Types
type Bits uint64

type Daemon struct {
	// bit/set representation of TuneD options which are remotely controllable.
	restart Bits
	// bit/set representation of Profile status conditions to report back via API.
	status Bits
	// stderr log from TuneD daemon to report back via API.
	stderr string
	// stopping is true while the controller tries to stop the TuneD daemon.
	stopping bool
	// recommendedProfile is the TuneD profile the operator calculated to be applied.
	// This variable is used to cache the value which was written to tunedRecommendFile.
	recommendedProfile string
}

type Change struct {
	// Did the node Profile k8s object change?
	profile bool
	// Do we need to update Tuned Profile status?
	profileStatus bool
	// Did the "rendered" Tuned k8s object change?
	rendered bool

	// The following keys are set when profile == true.
	// Was debugging set in Profile k8s object?
	debug bool
	// Cloud Provider as detected by the operator.
	provider string
	// Should we turn the reapply_sysctl TuneD option on in tuned-main.conf file?
	reapplySysctl bool
	// The current recommended profile as calculated by the operator.
	recommendedProfile string
}

type Controller struct {
	kubeconfig *restclient.Config
	kubeclient kubernetes.Interface

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens.
	wqKube  workqueue.RateLimitingInterface
	wqTuneD workqueue.RateLimitingInterface

	listers *ntoclient.Listers
	clients *ntoclient.Clients

	change Change
	daemon Daemon

	tunedCmd     *exec.Cmd       // external command (tuned) being prepared or run
	tunedExit    chan bool       // bi-directional channel to signal and register TuneD daemon exit
	stopCh       <-chan struct{} // receive-only channel to stop the openshift-tuned controller
	changeCh     chan bool       // bi-directional channel to wake-up the main thread to process accrued changes
	changeChRet  chan bool       // bi-directional channel to announce success/failure of change processing
	tunedMainCfg *ini.File       // global TuneD configuration as defined in tuned-main.conf
}

type wqKeyKube struct {
	kind string // object kind
	name string // object name
}

type wqKeyTuned struct {
	kind   string // object kind
	change Change // object change type
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
		kubeconfig:  kubeconfig,
		kubeclient:  kubeclient,
		listers:     listers,
		clients:     clients,
		tunedExit:   make(chan bool),
		stopCh:      stopCh,
		changeCh:    make(chan bool),
		changeChRet: make(chan bool),
	}

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

		klog.V(2).Infof("got event from workqueue: %#v", obj)
		func() {
			defer c.wqKube.Done(obj)
			var workqueueKey wqKeyKube
			var ok bool

			if workqueueKey, ok = obj.(wqKeyKube); !ok {
				c.wqKube.Forget(obj)
				klog.Errorf("expected wqKeyKube in workqueue but got %#v", obj)
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

func (c *Controller) sync(key wqKeyKube) error {
	switch {
	case key.kind == wqKindTuned:
		if key.name != tunedv1.TunedRenderedResourceName {
			return nil
		}
		klog.V(2).Infof("sync(): Tuned %s", key.name)

		// Notify the event processor that the Tuned k8s object containing all TuneD profiles changed.
		c.wqTuneD.Add(wqKeyTuned{kind: wqKindDaemon, change: Change{rendered: true}})

		return nil

	case key.kind == wqKindProfile:
		var change Change
		if key.name != getNodeName() {
			return nil
		}
		klog.V(2).Infof("sync(): Profile %s", key.name)

		profile, err := c.listers.TunedProfiles.Get(getNodeName())
		if err != nil {
			return fmt.Errorf("failed to get Profile %s: %v", key.name, err)
		}

		change.provider = profile.Spec.Config.ProviderName
		change.recommendedProfile = profile.Spec.Config.TunedProfile
		change.debug = profile.Spec.Config.Debug
		change.reapplySysctl = true
		if profile.Spec.Config.TuneDConfig.ReapplySysctl != nil {
			change.reapplySysctl = *profile.Spec.Config.TuneDConfig.ReapplySysctl
		}

		change.profile = true
		// Notify the event processor that the Profile k8s object containing information about which TuneD profile to apply changed.
		c.wqTuneD.Add(wqKeyTuned{kind: wqKindDaemon, change: change})

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

// ProfilesExtract extracts TuneD daemon profiles to the daemon configuration directory.
// Returns:
//   - True if the data in the to-be-extracted recommended profile or the profiles being
//     included from the current recommended profile have changed.
//   - A map with successfully extracted TuneD profile names.
//   - A map with names of TuneD profiles the current TuneD recommended profile depends on.
//   - Error if any or nil.
func ProfilesExtract(profiles []tunedv1.TunedProfile, recommendedProfile string) (bool, map[string]bool, map[string]bool, error) {
	var (
		change bool
	)
	klog.Infof("profilesExtract(): extracting %d TuneD profiles", len(profiles))

	recommendedProfileDeps := map[string]bool{}
	if len(recommendedProfile) > 0 {
		// Get a list of TuneD profiles names the recommended profile depends on.
		recommendedProfileDeps = profileDepends(recommendedProfile)
		// Add the recommended profile itself.
		recommendedProfileDeps[recommendedProfile] = true
	}
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
	change, extractedNew, recommendedProfileDeps, err := ProfilesExtract(profiles, recommendedProfile)
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

// Read the Cloud Provider name from openshiftTunedProvider file and
// extract/write 'provider' only if the file does not exist or it does not
// match 'provider'.  Returns indication whether the 'provider' changed and
// an error if any.
func providerSync(provider string) (bool, error) {
	var providerCurrent string

	f, err := os.Open(openshiftTunedProvider)
	if err != nil {
		if os.IsNotExist(err) {
			// The provider file hasn't been created yet.
			return len(provider) > 0, providerExtract(provider)
		}
		return false, err
	}
	defer f.Close()

	var scanner = bufio.NewScanner(f)
	for scanner.Scan() {
		providerCurrent = strings.TrimSpace(scanner.Text())
	}

	if provider != providerCurrent {
		return true, providerExtract(provider)
	}

	return false, nil
}

func TunedRecommendFileWrite(profileName string) error {
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

// overridenSysctl returns name of a host-level sysctl that overrides TuneD-level sysctl,
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
	return TunedCreateCmd((c.daemon.restart & ctrlDebug) != 0)
}

func (c *Controller) tunedRun() {
	klog.Infof("starting tuned...")

	defer func() {
		close(c.tunedExit)
	}()

	c.tunedExit = make(chan bool) // Once tunedStop() terminates, the tunedExit channel is closed!

	onDaemonReload := func() {
		// Notify the event processor that the TuneD daemon finished reloading and that we might need to update Profile status.
		c.wqTuneD.Add(wqKeyTuned{kind: wqKindDaemon, change: Change{profileStatus: true}})
	}

	err := TunedRun(c.tunedCmd, &c.daemon, onDaemonReload)
	if err != nil {
		klog.Errorf("Error while running tuned %v", err)
	}
}

// tunedStop tries to gracefully stop the TuneD daemon process by sending it SIGTERM.
// If the TuneD daemon does not respond by terminating within tunedGracefulExitWait
// duration, SIGKILL is sent.
func (c *Controller) tunedStop() error {
	c.daemon.stopping = true
	defer func() {
		c.daemon.stopping = false
		c.tunedCmd = nil // Cmd.Start() cannot be used more than once
	}()

	if c.tunedCmd == nil {
		// Looks like there has been a termination signal prior to starting tuned.
		return nil
	}
	if c.tunedCmd.Process != nil {
		// The TuneD daemon rolls back the current profile and should terminate on SIGTERM.
		klog.V(1).Infof("sending SIGTERM to PID %d", c.tunedCmd.Process.Pid)
		if err := c.tunedCmd.Process.Signal(syscall.SIGTERM); err != nil {
			if err == os.ErrProcessDone {
				// The TuneD process has already finished.
				return nil
			}
			return fmt.Errorf("failed to signal TuneD process: %v", err)
		}
	} else {
		// This should never happen!
		return fmt.Errorf("cannot find the TuneD process!")
	}
	// Wait for TuneD process to stop -- this will enable node-level tuning rollback.
	select {
	case <-c.tunedExit:
	case <-time.After(tunedGracefulExitWait):
		// It looks like the TuneD daemon refuses to terminate gracefully on SIGTERM
		// within tunedGracefulExitWait.
		klog.V(1).Infof("sending SIGKILL to PID %d", c.tunedCmd.Process.Pid)
		if err := c.tunedCmd.Process.Signal(syscall.SIGKILL); err != nil {
			if err == os.ErrProcessDone {
				// The TuneD process has already finished.
				return nil
			}
			return fmt.Errorf("failed to signal TuneD process: %v", err)
		}
		<-c.tunedExit
		return nil
	}
	klog.V(1).Infof("TuneD process terminated gracefully")

	return nil
}

func (c *Controller) tunedReload() error {
	klog.Infof("tunedReload()")

	c.daemon.status = 0 // clear the set out of which Profile status conditions are created
	c.daemon.status |= scReloading
	c.daemon.stderr = ""
	c.daemon.restart &= ^ctrlReload

	tunedStart := func() {
		c.tunedCmd = c.tunedCreateCmd()
		go c.tunedRun()
	}

	if c.tunedCmd == nil {
		// TuneD hasn't been started by openshift-tuned, start it.
		tunedStart()
		return nil
	}

	klog.Infof("reloading tuned...")

	if c.tunedCmd.Process != nil {
		klog.Infof("sending HUP to PID %d", c.tunedCmd.Process.Pid)
		if err := c.tunedCmd.Process.Signal(syscall.SIGHUP); err != nil {
			if err == os.ErrProcessDone {
				// The TuneD process has already finished.  The following comes to mind:
				//   * TuneD exitted due to a bug
				//   * someone or something (systemd) killed TuneD intentionally
				klog.Warningf("TuneD process PID %d finished", c.tunedCmd.Process.Pid)
				tunedStart()
				return nil
			}
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
func (c *Controller) tunedRestart() (err error) {
	klog.Infof("tunedRestart()")
	c.daemon.restart &= ^ctrlRestart

	if err = c.tunedStop(); err != nil {
		return err
	}

	if err = c.tunedReload(); err != nil {
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

func GetBootcmdline() (string, error) {
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

func (c *Controller) changeSyncerProfileStatus() (synced bool) {
	klog.V(2).Infof("changeSyncerProfileStatus()")

	if c.change.profileStatus {
		// One or both of the following happened:
		// 1) tunedBootcmdlineFile changed on the filesystem.  This is very likely the result of
		//    applying a TuneD profile by the TuneD daemon.  Make sure the node Profile k8s object
		//    is in sync with tunedBootcmdlineFile so the operator can take an appropriate action.
		// 2) TuneD daemon was reloaded.  Make sure the node Profile k8s object is in sync with
		//    the active profile, e.g. the Profile indicates the presence of the stall daemon on
		//    the host if requested by the current active profile.
		if err := c.updateTunedProfile(); err != nil {
			klog.Error(err.Error())
			return false // retry later
		}
	}

	return true
}

// changeSyncerTuneD synchronizes k8s objects to disk, compares them with
// current TuneD configuration and signals TuneD process reload or restart.
func (c *Controller) changeSyncerTuneD() (synced bool, err error) {
	var reload bool

	klog.V(2).Infof("changeSyncerTuneD()")

	if (c.daemon.status & scReloading) != 0 {
		// This should not be necessary, but keep this here as a reminder.
		// We should not manipulate TuneD configuration files in any way while the daemon is reloading/restarting.
		return false, fmt.Errorf("changeSyncerTuneD(): called while the TuneD daemon was reloading")
	}

	// Check whether reload of the TuneD daemon is really necessary due to a Profile change.
	if c.change.profile {
		changeProvider, err := providerSync(c.change.provider)
		if err != nil {
			return false, err
		}
		reload = reload || changeProvider

		if c.daemon.recommendedProfile != c.change.recommendedProfile {
			if err = TunedRecommendFileWrite(c.change.recommendedProfile); err != nil {
				return false, err
			}
			klog.Infof("recommended TuneD profile changed from (%s) to (%s)", c.daemon.recommendedProfile, c.change.recommendedProfile)
			// Cache the value written to tunedRecommendFile.
			c.daemon.recommendedProfile = c.change.recommendedProfile
			reload = true
		} else {
			klog.Infof("recommended profile (%s) matches current configuration", c.daemon.recommendedProfile)
			// We do not need to reload the TuneD daemon, however, someone may have tampered with the k8s Profile status for this node.
			// Make sure its status is up-to-date.
			if err = c.updateTunedProfile(); err != nil {
				klog.Error(err.Error())
				return false, nil // retry later
			}
		}

		// Does the current TuneD process have debugging turned on?
		debug := (c.daemon.restart & ctrlDebug) != 0
		if debug != c.change.debug {
			// A complete restart of the TuneD daemon is needed due to a debugging request switched on or off.
			c.daemon.restart |= ctrlRestart
			if c.change.debug {
				c.daemon.restart |= ctrlDebug
			} else {
				c.daemon.restart &= ^ctrlDebug
			}
		}

		// Does the current TuneD process have the reapply_sysctl option turned on?
		reapplySysctl := c.tunedMainCfg.Section("").Key("reapply_sysctl").MustBool()
		if reapplySysctl != c.change.reapplySysctl {
			if err = iniCfgSetKey(c.tunedMainCfg, "reapply_sysctl", !reapplySysctl); err != nil {
				return false, err
			}
			err = iniFileSave(tunedProfilesDirCustom+"/"+tunedMainConfFile, c.tunedMainCfg)
			if err != nil {
				return false, fmt.Errorf("failed to write global TuneD configuration file: %v", err)
			}
			c.daemon.restart |= ctrlRestart // A complete restart of the TuneD daemon is needed due to configuration change in tuned-main.conf file.
		}
	}

	if c.change.rendered {
		// The "rendered" Tuned k8s object changed.
		tuned, err := c.listers.TunedResources.Get(tunedv1.TunedRenderedResourceName)
		if err != nil {
			klog.Errorf("failed to get Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
			return false, nil // retry later
		}

		changeRendered, err := profilesSync(tuned.Spec.Profile, c.daemon.recommendedProfile)
		if err != nil {
			return false, err
		}

		reload = reload || changeRendered
	}

	if reload {
		c.daemon.restart |= ctrlReload
	}
	err = c.changeSyncerRestartOrReloadTuneD()

	return err == nil, err
}

func (c *Controller) changeSyncerRestartOrReloadTuneD() error {
	var err error

	klog.V(2).Infof("changeSyncerRestartOrReloadTuneD()")

	if (c.daemon.restart & ctrlRestart) != 0 {
		// Complete restart of the TuneD daemon needed.  For example, debuging option is used or an option in tuned-main.conf file changed).
		err = c.tunedRestart()
		return err
	}

	if (c.daemon.restart & ctrlReload) != 0 {
		err = c.tunedReload()
	}

	return err
}

// Method changeSyncer performs k8s Profile object updates and TuneD daemon
// reloads as needed.  Returns indication whether the change was successfully
// synced and an error.  Only critical errors are returned, as non-nil errors
// will cause restart of the main control loop -- the changeWatcher() method.
func (c *Controller) changeSyncer() (synced bool, err error) {
	// Sync k8s Profile status if/when needed.
	if !c.changeSyncerProfileStatus() {
		return false, nil
	}

	// Extract TuneD configuration from k8s objects and restart/reload TuneD as needed.
	return c.changeSyncerTuneD()
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

		klog.V(2).Infof("got event from workqueue: %#v", obj)
		func() {
			defer c.wqTuneD.Done(obj)
			var workqueueKey wqKeyTuned
			var ok bool

			if workqueueKey, ok = obj.(wqKeyTuned); !ok {
				c.wqTuneD.Forget(obj)
				klog.Errorf("expected wqKeyTuned in workqueue but got %#v", obj)
				return
			}

			if (c.daemon.status & scReloading) != 0 {
				// Do not do any of the following until TuneD finished with the previous reload:
				//   * update Profile status
				//   * extract TuneD configuration
				//   * reload/restart TuneD
				// Requeuing based on TuneD reloading is far from ideal, but it is simple/reliable.
				// When rewriting this, beware of rare deadlocks.
				c.wqTuneD.AddRateLimited(workqueueKey)
				return
			}

			c.change = workqueueKey.change
			c.changeCh <- true
			eventProcessed := <-c.changeChRet
			if !eventProcessed {
				requeued := c.wqTuneD.NumRequeues(workqueueKey)
				// Limit retries to maxRetries.  After that, stop trying.
				if requeued < maxRetries {
					klog.Errorf("unable to sync(%s/%+v) requeued (%d)", workqueueKey.kind, workqueueKey.change, requeued)

					// Re-enqueue the workqueueKey.  Based on the rate limiter on the queue
					// and the re-enqueue history, the workqueueKey will be processed later again.
					c.wqTuneD.AddRateLimited(workqueueKey)
					return
				}
				klog.Errorf("unable to sync(%s/%+v) reached max retries (%d)", workqueueKey.kind, workqueueKey.change, maxRetries)
				// Dropping the item after maxRetries unsuccessful retries.
				c.wqTuneD.Forget(obj)
				return
			}
			klog.V(1).Infof("event from workqueue (%s/%+v) successfully processed", workqueueKey.kind, workqueueKey.change)
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
	klog.Infof("updated Node %v annotation", node.Name)

	return nil
}

// Method updateTunedProfile updates a Tuned Profile with information to report back
// to the operator.  Note this method must be called only when the TuneD daemon is
// not reloading.
func (c *Controller) updateTunedProfile() (err error) {
	var (
		bootcmdline string
	)

	if bootcmdline, err = GetBootcmdline(); err != nil {
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

	if !bootcmdlineAnnotSet || bootcmdlineAnnotVal != bootcmdline {
		annotations := map[string]string{tunedv1.TunedBootcmdlineAnnotationKey: bootcmdline}
		err = c.updateNodeAnnotations(node, annotations)
		if err != nil {
			return err
		}
	}

	if profile.Status.TunedProfile == activeProfile &&
		conditionsEqual(profile.Status.Conditions, statusConditions) {
		klog.V(2).Infof("updateTunedProfile(): no need to update status of Profile %s", profile.Name)
		return nil
	}

	profile = profile.DeepCopy() // never update the objects from cache

	profile.Status.TunedProfile = activeProfile
	profile.Status.Conditions = statusConditions
	_, err = c.clients.Tuned.TunedV1().Profiles(operandNamespace).UpdateStatus(context.TODO(), profile, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Profile %s status: %v", profile.Name, err)
	}

	return nil
}

func (c *Controller) informerEventHandler(workqueueKey wqKeyKube) cache.ResourceEventHandlerFuncs {
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
			c.wqKube.Add(wqKeyKube{kind: workqueueKey.kind, name: workqueueKey.name})
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
			c.wqKube.Add(wqKeyKube{kind: workqueueKey.kind, name: newAccessor.GetName()})
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
			c.wqKube.Add(wqKeyKube{kind: workqueueKey.kind, name: object.GetName()})
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
	if _, err = trInformer.Informer().AddEventHandler(c.informerEventHandler(wqKeyKube{kind: wqKindTuned})); err != nil {
		return err
	}

	tpInformer := tunedInformerFactory.Tuned().V1().Profiles()
	c.listers.TunedProfiles = tpInformer.Lister().Profiles(operandNamespace)
	if _, err = tpInformer.Informer().AddEventHandler(c.informerEventHandler(wqKeyKube{kind: wqKindProfile})); err != nil {
		return err
	}

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

		case fsEvent := <-wFs.Events:
			klog.V(2).Infof("fsEvent")
			if fsEvent.Op&fsnotify.Write == fsnotify.Write {
				klog.V(1).Infof("write event on: %s", fsEvent.Name)
				// Notify the event processor that the TuneD daemon calculated new kernel command-line parameters.
				c.wqTuneD.Add(wqKeyTuned{kind: wqKindDaemon, change: Change{profileStatus: true}})
			}

		case err := <-wFs.Errors:
			return fmt.Errorf("error watching filesystem: %v", err)

		case <-c.changeCh:
			var synced bool
			klog.V(2).Infof("changeCh")

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
			if err := c.tunedStop(); err != nil {
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

func RunOperand(stopCh <-chan struct{}, version string, inCluster bool) error {
	klog.Infof("starting %s %s; in-cluster: %v", programName, version, inCluster)

	c, err := newController(stopCh)
	if err != nil {
		// This looks really bad, there was an error creating the Controller.
		panic(err.Error())
	}

	return retryLoop(c)
}
