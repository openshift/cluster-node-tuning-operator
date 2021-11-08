package tuned

import (
	"bufio"     // scanner
	"bytes"     // bytes.Buffer
	"context"   // context.TODO()
	"flag"      // command-line options parsing
	"fmt"       // Printf()
	"io/ioutil" // ioutil.ReadFile()
	"math"      // math.Pow()
	"net"       // net.Conn
	"os"        // os.Exit(), os.Stderr, ...
	"os/exec"   // os.Exec()
	"strconv"   // strconv
	"strings"   // strings.Join()
	"syscall"   // syscall.SIGHUP, ...
	"time"      // time.Second, ...

	"gopkg.in/ini.v1"
	kmeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	fsnotify "gopkg.in/fsnotify.v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	tunedset "github.com/openshift/cluster-node-tuning-operator/pkg/generated/clientset/versioned"
	tunedinformers "github.com/openshift/cluster-node-tuning-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
)

// Constants
const (
	// Constants used for instantiating Profile status conditions;
	// they will be set to 2^0, 2^1, 2^2, ..., 2^n
	scApplied Bits = 1 << iota
	scWarn
	scError
)

// Constants
const (
	operandNamespace       = "openshift-cluster-node-tuning-operator"
	timedUpdaterInterval   = 1
	programName            = "openshift-tuned"
	tunedProfilesDir       = "/etc/tuned"
	tunedActiveProfileFile = tunedProfilesDir + "/active_profile"
	tunedRecommendDir      = tunedProfilesDir + "/recommend.d"
	tunedRecommendFile     = tunedRecommendDir + "/50-openshift.conf"
	tunedBootcmdlineEnvVar = "TUNED_BOOT_CMDLINE"
	tunedBootcmdlineFile   = tunedProfilesDir + "/bootcmdline"
	// a couple of seconds should be more than enough for Tuned daemon to gracefully stop;
	// be generous and give it 10s
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
	tunedInitialTimeout   = 60 // timeout in seconds
	openshiftTunedRunDir  = "/run/" + programName
	openshiftTunedPidFile = openshiftTunedRunDir + "/" + programName + ".pid"
	openshiftTunedSocket  = "/var/lib/tuned/openshift-tuned.sock"
	// With the DefaultControllerRateLimiter, retries will happen at 5ms*2^(retry_n-1)
	// 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s, 5.1s, 10.2s, 20.4s, 41s, 82s
	maxRetries = 15
	// workqueue related constants
	wqKindTuned   = "tuned"
	wqKindProfile = "profile"
)

// Types
type arrayFlags []string

type Bits uint8

type sockAccepted struct {
	conn net.Conn
	err  error
}

type Controller struct {
	kubeconfig *restclient.Config

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens.
	workqueue workqueue.RateLimitingInterface

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
		// Did the command-line parameters to run the Tuned daemon change?
		// In other words, is a complete restart of the Tuned daemon needed?
		daemon bool
	}

	daemon struct {
		// reloading is true during the Tuned daemon reload
		reloading bool
		// reloaded is true immediately after the Tuned daemon finished reloading
		// and the node Profile k8s object's Status needs to be set for the operator;
		// it is set to false on successful Profile update
		reloaded bool
		// debugging flag
		debug bool
		// bit/set representaton of Profile status conditions to report back via API
		status Bits
		// stopping is true while the controller tries to stop the TuneD daemon.
		stopping bool
	}

	tunedCmd     *exec.Cmd       // external command (tuned) being prepared or run
	tunedExit    chan bool       // bi-directional channel to signal and register Tuned daemon exit
	stopCh       <-chan struct{} // receive-only channel to stop the openshift-tuned controller
	tunedTicker  *time.Ticker    // ticker that fires if TuneD daemon fails to report "profile XYZ applied" within tunedTimeout
	tunedTimeout int             // timeout for TuneD daemon to report "profile applied/reload failed" [s]
}

type wqKey struct {
	kind  string // object kind
	name  string // object name
	event string // object event type (add/update/delete) or pass the full object on delete
}

// Functions
func mkdir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, os.ModePerm)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *arrayFlags) String() string {
	return strings.Join(*a, ",")
}

func (a *arrayFlags) Set(value string) error {
	*a = append(*a, value)
	return nil
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

func newController(stopCh <-chan struct{}) (*Controller, error) {
	kubeconfig, err := ntoclient.GetConfig()
	if err != nil {
		return nil, err
	}

	listers := &ntoclient.Listers{}
	clients := &ntoclient.Clients{}
	controller := &Controller{
		kubeconfig:  kubeconfig,
		workqueue:   workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		listers:     listers,
		clients:     clients,
		tunedExit:   make(chan bool, 1),
		stopCh:      stopCh,
		tunedTicker: time.NewTicker(tunedInitialTimeout),
	}
	controller.tunedTicker.Stop() // The ticker will be started/reset when TuneD starts.

	return controller, nil
}

// eventProcessor is a long-running method that will continually
// read and process a message on the workqueue.
func (c *Controller) eventProcessor() {
	for {
		// Wait until there is a new item in the working queue
		obj, shutdown := c.workqueue.Get()
		if shutdown {
			return
		}

		klog.V(2).Infof("got event from workqueue")
		func() {
			defer c.workqueue.Done(obj)
			var workqueueKey wqKey
			var ok bool

			if workqueueKey, ok = obj.(wqKey); !ok {
				c.workqueue.Forget(obj)
				klog.Errorf("expected wqKey in workqueue but got %#v", obj)
				return
			}

			if err := c.sync(workqueueKey); err != nil {
				// Limit retries to maxRetries.  After that, stop trying.
				if c.workqueue.NumRequeues(workqueueKey) < maxRetries {
					// Re-enqueue the workqueueKey.  Based on the rate limiter on the queue
					// and the re-enqueue history, the workqueueKey will be processed later again.
					c.workqueue.AddRateLimited(workqueueKey)
					klog.Errorf("unable to sync(%s/%s) requeued: %v", workqueueKey.kind, workqueueKey.name, err)
					return
				}
				klog.Errorf("unable to sync(%s/%s) reached max retries(%d): %v", workqueueKey.kind, workqueueKey.name, maxRetries, err)
			} else {
				klog.V(1).Infof("event from workqueue (%s/%s) successfully processed", workqueueKey.kind, workqueueKey.name)
			}
			// Successful processing or we're dropping an item after maxRetries unsuccessful retries
			c.workqueue.Forget(obj)
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

		change, err := profilesExtract(tuned.Spec.Profile)
		if err != nil {
			return err
		}
		c.change.rendered = change

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

		err = tunedRecommendFileWrite(profile.Spec.Config.TunedProfile)
		if err != nil {
			return err
		}
		c.change.profile = true

		if c.daemon.debug != profile.Spec.Config.Debug {
			c.change.daemon = true // A complete restart of the Tuned daemon is needed due to a debugging request switched on or off.
			c.daemon.debug = profile.Spec.Config.Debug
		}

		return nil

	default:
	}

	return nil
}

func newUnixListener(addr string) (net.Listener, error) {
	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}
	return l, nil
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

// profilesExtract extracts Tuned daemon profiles to the daemon configuration directory.
// If the data in the to be extracted recommended profile is different than the current
// recommended profile, the function returns true.
func profilesExtract(profiles []tunedv1.TunedProfile) (bool, error) {
	var (
		change bool
	)
	klog.Infof("extracting Tuned profiles")

	recommendedProfile, err := getRecommendedProfile()
	if err != nil {
		return change, err
	}
	for index, profile := range profiles {
		if profile.Name == nil {
			klog.Warningf("profilesExtract(): profile name missing for Profile %v", index)
			continue
		}
		if profile.Data == nil {
			klog.Warningf("profilesExtract(): profile data missing for Profile %v", index)
			continue
		}
		profileDir := fmt.Sprintf("%s/%s", tunedProfilesDir, *profile.Name)
		profileFile := fmt.Sprintf("%s/%s", profileDir, "tuned.conf")

		if err := mkdir(profileDir); err != nil {
			return change, fmt.Errorf("failed to create Tuned profile directory %q: %v", profileDir, err)
		}

		if recommendedProfile == *profile.Name {
			// Recommended profile name matches profile of the profile currently being extracted, compare their content.
			var un string
			content, err := ioutil.ReadFile(profileFile)
			if err != nil {
				content = []byte{}
			}

			change = *profile.Data != string(content)
			if !change {
				un = "un"
			}
			klog.Infof("recommended Tuned profile %s content %schanged", recommendedProfile, un)
		}

		f, err := os.Create(profileFile)
		if err != nil {
			return change, fmt.Errorf("failed to create Tuned profile file %q: %v", profileFile, err)
		}
		defer f.Close()
		if _, err = f.WriteString(*profile.Data); err != nil {
			return change, fmt.Errorf("failed to write Tuned profile file %q: %v", profileFile, err)
		}
	}

	return change, nil
}

func openshiftTunedPidFileWrite() error {
	if err := mkdir(openshiftTunedRunDir); err != nil {
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
	if err := mkdir(tunedRecommendDir); err != nil {
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
	klog.Infof("written %q to set Tuned profile %s", tunedRecommendFile, profileName)
	return nil
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
			profileApplied := strings.Index(l, " tuned.daemon.daemon: static tuning from profile ") >= 0 && strings.Index(l, " applied") >= 0
			reloadFailed := strings.Index(l, " tuned.daemon.controller: Failed to reload Tuned: ") >= 0

			if profileApplied {
				c.daemon.status |= scApplied
				c.tunedTicker.Stop() // profile successfully applied, stop the TuneD watcher
			}

			if strings.Index(l, " WARNING ") >= 0 {
				c.daemon.status |= scWarn
			}

			if strings.Index(l, " ERROR ") >= 0 {
				c.daemon.status |= scError
			}

			if c.daemon.reloading {
				c.daemon.reloading = !profileApplied && !reloadFailed
				c.daemon.reloaded = !c.daemon.reloading
			}

			fmt.Printf("%s\n", l)
		}
	}()

	c.daemon.reloading = true
	c.daemon.status = 0 // clear the set out of which Profile status conditions are created
	if err = c.tunedCmd.Start(); err != nil {
		klog.Errorf("error starting tuned: %v", err)
		return
	}

	if err = c.tunedCmd.Wait(); err != nil {
		// The command exited with non 0 exit status, e.g. terminated by a signal
		klog.Errorf("error waiting for tuned: %v", err)
		return
	}

	return
}

// tunedStop tries to gracefully stop the Tuned daemon process by sending it SIGTERM.
// If the Tuned daemon does not respond by terminating within tunedGracefulExitWait
// duration, SIGKILL is sent.  This method returns an indication whether the TuneD
// daemon exitted gracefully (true) or SIGKILL had to be sent (false).
func (c *Controller) tunedStop() (bool, error) {
	c.daemon.stopping = true
	defer func() {
		c.daemon.stopping = false
	}()

	if c.tunedCmd == nil {
		// Looks like there has been a termination signal prior to starting tuned
		return false, nil
	}
	if c.tunedCmd.Process != nil {
		// The Tuned daemon rolls back the current profile and should terminate on SIGTERM
		klog.V(1).Infof("sending SIGTERM to PID %d", c.tunedCmd.Process.Pid)
		c.tunedCmd.Process.Signal(syscall.SIGTERM)
	} else {
		// This should never happen
		return false, fmt.Errorf("cannot find the Tuned process!")
	}
	// Wait for Tuned process to stop -- this will enable node-level tuning rollback
	select {
	case <-c.tunedExit:
	case <-time.After(tunedGracefulExitWait):
		// It looks like the Tuned daemon refuses to terminate gracefully on SIGTERM
		// within tunedGracefulExitWait
		klog.V(1).Infof("sending SIGKILL to PID %d", c.tunedCmd.Process.Pid)
		c.tunedCmd.Process.Signal(syscall.SIGKILL)
		<-c.tunedExit
		return false, nil
	}
	klog.V(1).Infof("Tuned process terminated gracefully")

	return true, nil
}

func (c *Controller) tunedReload(timeoutInitiated bool) error {
	c.daemon.reloading = true
	c.daemon.status = 0 // clear the set out of which Profile status conditions are created
	tunedTimeout := time.Second * time.Duration(tunedInitialTimeout)
	if c.tunedTicker == nil {
		// This should never happen as the ticker is initialized at controller creation time.
		c.tunedTicker = time.NewTicker(tunedTimeout)
	} else {
		c.tunedTicker.Reset(tunedTimeout)
	}

	if c.tunedCmd == nil {
		// TuneD hasn't been started by openshift-tuned, start it
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
		// This should never happen
		return fmt.Errorf("cannot find the Tuned process!")
	}

	return nil
}

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

// getActiveProfile returns active profile currently in use by the Tuned daemon.
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

func getRecommendedProfile() (string, error) {
	var stdout, stderr bytes.Buffer

	klog.V(1).Infof("getting recommended profile...")
	cmd := exec.Command("/usr/sbin/tuned-adm", "recommend")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("error getting recommended profile: %v: %v", err, stderr.String())
	}

	responseString := strings.TrimSpace(stdout.String())
	return responseString, nil
}

// Method timedUpdater is called every timedUpdaterInterval seconds provided
// the Tuned daemon is not already reloading.  It performs k8s Profile object
// updates and Tuned daemon reloads as needed.
func (c *Controller) timedUpdater() (err error) {
	var reload bool

	if c.daemon.reloading {
		// This should not be necessary, but keep this here as a reminder.
		return fmt.Errorf("timedUpdater(): called while the Tuned daemon was reloading")
	}

	if c.change.daemon {
		// Complete restart of the Tuned daemon needed.
		c.change.daemon = false
		c.change.profile = false
		return c.tunedRestart(false)
	}

	if c.change.bootcmdline || c.daemon.reloaded {
		// One or both of the following happened:
		// 1) tunedBootcmdlineFile changed on the filesystem.  This is very likely the result of
		//    applying a Tuned profile by the Tuned daemon.  Make sure the node Profile k8s object
		//    is in sync with tunedBootcmdlineFile so the operator can take an appropriate action.
		// 2) Tuned daemon was reloaded.  Make sure the node Profile k8s object is in sync with
		//    the active profile, e.g. the Profile indicates the presence of the stall daemon on
		//    the host if requested by the current active profile.
		if err = c.updateTunedProfile(); err != nil {
			// Log this and retry later.
			klog.Error(err.Error())
		} else {
			// The node Profile k8s object was updated successfully.  Clear the flags indicating
			// a check for syncing the object is needed.
			c.change.bootcmdline = false
			c.daemon.reloaded = false
		}
	}

	// Check whether reload of the Tuned daemon is really necessary due to a Profile change
	if c.change.profile {
		// The node Profile k8s object changed
		var activeProfile, recommendedProfile string
		if activeProfile, err = getActiveProfile(); err != nil {
			return err
		}
		if recommendedProfile, err = getRecommendedProfile(); err != nil {
			return err
		}
		if (c.daemon.status & scApplied) == 0 {
			klog.Infof("re-applying profile (%s) as the previous application did not complete", activeProfile)
			reload = true
		} else if (c.daemon.status & scError) != 0 {
			klog.Infof("re-applying profile (%s) as the previous application ended with error(s)", activeProfile)
			reload = true
		} else if activeProfile != recommendedProfile {
			klog.Infof("active profile (%s) != recommended profile (%s)", activeProfile, recommendedProfile)
			recommendedProfileDir := tunedProfilesDir + "/" + recommendedProfile
			if _, err := os.Stat(recommendedProfileDir); os.IsNotExist(err) {
				// Workaround for tuned BZ1774645; do not send SIGHUP to tuned if the profile directory doesn't exist.
				// Log this as an error for easier debugging.  Persistent non-existence of the profile directory very
				// likely indicates a custom user profile which references a profile that was not defined.
				klog.Errorf("Tuned profile directory %q does not exist; was %q defined?", recommendedProfileDir, recommendedProfile)
				return nil // retry later
			}
			reload = true
		} else {
			klog.Infof("active and recommended profile (%s) match; profile change will not trigger profile reload", activeProfile)
		}
		c.change.profile = false
	}
	if c.change.rendered {
		// The "rendered" Tuned k8s object changed.
		c.change.rendered = false
		reload = true
	}

	if reload {
		err = c.tunedReload(false)
	}
	return err
}

func getTuned(obj interface{}) (tuned *tunedv1.Tuned, err error) {
	tuned, ok := obj.(*tunedv1.Tuned)
	if !ok {
		return nil, fmt.Errorf("could not convert object to a Tuned object: %+v", obj)
	}
	return tuned, nil
}

func getTunedProfile(obj interface{}) (profile *tunedv1.Profile, err error) {
	profile, ok := obj.(*tunedv1.Profile)
	if !ok {
		return nil, fmt.Errorf("could not convert object to a Tuned Profile object: %+v", obj)
	}

	return profile, nil
}

func getNodeName() string {
	name := os.Getenv("OCP_NODE_NAME")
	if len(name) == 0 {
		// Something is seriously wrong, OCP_NODE_NAME must be defined via Tuned DaemonSet
		panic("OCP_NODE_NAME unset or empty")
	}
	return name
}

// getIniFileSectionSlice searches INI file `data` inside [`section`]
// for key `key`.  It takes the key's value and uses separator
// `separator` to return a slice of strings.
func getIniFileSectionSlice(data *string, section, key, separator string) []string {
	var ret []string

	if data == nil {
		return ret
	}

	cfg, err := ini.Load([]byte(*data))
	if err != nil {
		// This looks like an invalid INI data or parser error.
		klog.Errorf("unable to read INI file data: %v", err)
		return ret
	}

	if !cfg.Section(section).HasKey(key) {
		return ret
	}

	ret = strings.Split(cfg.Section(section).Key(key).String(), separator)

	return ret
}

// profileHasStalld returns pointer to a boolean value.  The dereferenced
// pointer value depends on the Tuned [service] plugin enabling/disabling
// the "stalld" service.  If the "stalld" service "service.stalld" key is
// not found in Tuned [service] section, nil is returned.
func profileHasStalld(profile *string) *bool {
	var ret bool

	stalldServiceVal := getIniFileSectionSlice(profile, "service", "service.stalld", ",")

	if len(stalldServiceVal) == 0 {
		// there was no service.stalld key in [service] section
		return nil
	}

	for _, v := range stalldServiceVal {
		if v == "enable" {
			ret = true
			return &ret
		}
		if v == "disable" {
			return &ret
		}
	}

	// default to "disable" if "enable" is not present as value by service.stalld key
	return &ret
}

func (c *Controller) stalldRequested(profileName string) (*bool, error) {
	var ret bool

	tuned, err := c.listers.TunedResources.Get(tunedv1.TunedRenderedResourceName)
	if err != nil {
		return &ret, fmt.Errorf("failed to get Tuned %s: %v", tunedv1.TunedRenderedResourceName, err)
	}

	for index, profile := range tuned.Spec.Profile {
		if profile.Name == nil {
			klog.Warningf("tunedHasStalld(): profile name missing for Profile %v", index)
			continue
		}
		if *profile.Name != profileName {
			continue
		}
		if profile.Data == nil {
			klog.Warningf("tunedHasStalld(): profile data missing for Profile %v", index)
			continue
		}
		return profileHasStalld(profile.Data), nil
	}

	return &ret, nil
}

// Method updateTunedProfile updates a Tuned profile with information to report back
// to the operator.  Note this method must be called only when the Tuned daemon is
// not reloading.
func (c *Controller) updateTunedProfile() (err error) {
	var (
		bootcmdline     string
		stalldRequested *bool
	)

	if bootcmdline, err = getBootcmdline(); err != nil {
		// This should never happen unless something is seriously wrong (e.g. Tuned
		// daemon no longer uses tunedBootcmdlineFile).  Do not continue.
		return fmt.Errorf("unable to get kernel command-line parameters: %v", err)
	}

	profile, err := c.listers.TunedProfiles.Get(getNodeName())
	if err != nil {
		return fmt.Errorf("failed to get Profile %s: %v", profile.Name, err)
	}

	if stalldRequested, err = c.stalldRequested(profile.Spec.Config.TunedProfile); err != nil {
		return fmt.Errorf("unable to assess whether stalld is requested: %v", err)
	}

	stalldUnchanged := util.PtrBoolEqual(profile.Status.Stalld, stalldRequested)

	if profile.Status.Bootcmdline == bootcmdline && stalldUnchanged {
		// Do not update node profile unnecessarily (e.g. bootcmdline did not change)
		klog.V(2).Infof("updateTunedProfile(): no need to update Profile %s", profile.Name)
		return nil
	}

	profile = profile.DeepCopy() // never update the objects from cache

	profile.Status.Bootcmdline = bootcmdline
	profile.Status.Stalld = stalldRequested
	_, err = c.clients.Tuned.TunedV1().Profiles(operandNamespace).Update(context.TODO(), profile, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Profile %s status: %v", profile.Name, err)
	}
	klog.Infof("updated Profile %s stalld=%v, bootcmdline: %s", profile.Name, stalldRequested, bootcmdline)

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
			c.workqueue.Add(wqKey{kind: workqueueKey.kind, name: workqueueKey.name})
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
			c.workqueue.Add(wqKey{kind: workqueueKey.kind, name: newAccessor.GetName()})
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
			c.workqueue.Add(wqKey{kind: workqueueKey.kind, name: object.GetName()})
		},
	}
}

// The changeWatcher method is the main control loop watching for changes to be applied
// and supervising the Tuned daemon.  On successful (error == nil) exit, no attempt at
// reentering this control loop should be made as it is an indication of an intentional
// exit on request.
func (c *Controller) changeWatcher() (err error) {
	var (
		lStop bool
	)

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
	trInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindTuned}))

	tpInformer := tunedInformerFactory.Tuned().V1().Profiles()
	c.listers.TunedProfiles = tpInformer.Lister().Profiles(operandNamespace)
	tpInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindProfile}))

	tunedInformerFactory.Start(c.stopCh) // Tuned/Profile

	// Wait for the caches to be synced before starting worker(s)
	klog.V(1).Info("waiting for informer caches to sync")
	ok := cache.WaitForCacheSync(c.stopCh,
		trInformer.Informer().HasSynced,
		tpInformer.Informer().HasSynced,
	)
	if !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.V(1).Info("starting events processor")
	go wait.Until(c.eventProcessor, time.Second, c.stopCh)
	klog.Info("started events processor")

	// Create a ticker to extract new Tuned profiles and possibly reload tuned;
	// this also rate-limits reloads to a maximum of timedUpdaterInterval reloads/s
	tickerUpdate := time.NewTicker(time.Second * time.Duration(timedUpdaterInterval))
	defer tickerUpdate.Stop()

	// Watch for filesystem changes on the tunedBootcmdlineFile file
	wFs, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create filesystem watcher: %v", err)
	}
	defer wFs.Close()

	// Register fsnotify watchers
	for _, element := range []string{tunedBootcmdlineFile} {
		err = wFs.Add(element)
		if err != nil {
			return fmt.Errorf("failed to start watching %q: %v", element, err)
		}
	}

	l, err := newUnixListener(openshiftTunedSocket)
	if err != nil {
		return fmt.Errorf("cannot create %q listener: %v", openshiftTunedSocket, err)
	}
	defer func() {
		lStop = true
		l.Close()
	}()

	sockConns := make(chan sockAccepted, 1)
	go func() {
		for {
			conn, err := l.Accept()
			if lStop {
				// The listener was closed on the return from mainLoop(); exit the goroutine
				return
			}
			sockConns <- sockAccepted{conn, err}
		}
	}()

	klog.Info("started controller")
	for {
		select {
		case <-c.stopCh:
			klog.Infof("termination signal received, stop")

			return nil

		case s := <-sockConns:
			const socketCmdStop = "stop"
			var rolledBack bool

			if s.err != nil {
				return fmt.Errorf("connection accept error: %v", err)
			}

			buf := make([]byte, len(socketCmdStop))
			nr, _ := s.conn.Read(buf)
			data := buf[0:nr]

			if string(data) != socketCmdStop {
				// We only support one command over the socket interface at this point
				klog.Warningf("ignoring unsupported command received over socket: %s", string(data))
				continue
			}

			// At this point we know there was a request to exit, do not return any more errors,
			// just log them.
			if rolledBack, err = c.tunedStop(); err != nil {
				klog.Errorf("%s", err.Error())
			}
			resp := make([]byte, 2)
			if rolledBack {
				// Indicate a successful settings rollback
				resp = append(resp, 'o', 'k')
			}
			_, err := s.conn.Write(resp)
			if err != nil {
				klog.Errorf("cannot write a response via %q: %v", openshiftTunedSocket, err)
			}
			return nil

		case <-c.tunedExit:
			c.tunedCmd = nil // Cmd.Start() cannot be used more than once
			klog.Infof("Tuned process exitted...")

			// Do not be too aggressive about keeping the Tuned daemon around.
			// Tuned daemon might have exitted after receiving SIGTERM during
			// system reboot/shutdown.
			return nil

		case fsEvent := <-wFs.Events:
			klog.V(2).Infof("fsEvent")
			if fsEvent.Op&fsnotify.Write == fsnotify.Write {
				klog.V(1).Infof("write event on: %s", fsEvent.Name)
				c.change.bootcmdline = true
			}

		case err := <-wFs.Errors:
			return fmt.Errorf("error watching filesystem: %v", err)

		case <-c.tunedTicker.C:
			klog.Errorf("timeout (%d) to apply TuneD profile; restarting TuneD daemon", tunedInitialTimeout)
			err := c.tunedRestart(true)
			if err != nil {
				return err
			}
			// TuneD profile application is failing, make this visible in "oc get profile" output.
			if err = c.updateTunedProfile(); err != nil {
				klog.Error(err.Error())
			}

		case <-tickerUpdate.C:
			klog.V(2).Infof("tickerUpdate.C")

			if c.daemon.stopping {
				// We have decided to stop TuneD.  Apart from showing the logs it is
				// now unnecessary/undesirable to perform any of the following actions.
				// The undesirability comes from extra processing which will come if
				// TuneD manages to "get unstuck" during this phase before it receives
				// SIGKILL (note the time window between SIGTERM/SIGKILL).
				klog.Infof("c.daemon.stopping...")
				continue
			}

			if c.daemon.reloading {
				// Do not reload the Tuned daemon unless it finished reloading
				continue
			}

			if err := c.timedUpdater(); err != nil {
				return err
			}
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
			// This should never happen
			klog.Errorf("cannot find the Tuned process!")
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
		// This looks really bad, there was an error creating the Controller
		panic(err.Error())
	}

	err = retryLoop(c)
	if err != nil {
		panic(err.Error())
	}
}
