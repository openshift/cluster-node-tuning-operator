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
	"os"        // os.Exit(), os.Signal, os.Stderr, ...
	"os/exec"   // os.Exec()
	"strconv"   // strconv
	"strings"   // strings.Join()
	"syscall"   // syscall.SIGHUP, ...
	"time"      // time.Second, ...

	kmeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	"github.com/fsnotify/fsnotify"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	tunedset "github.com/openshift/cluster-node-tuning-operator/pkg/generated/clientset/versioned"
	tunedinformers "github.com/openshift/cluster-node-tuning-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
)

// Constants
const (
	operandNamespace       = "openshift-cluster-node-tuning-operator"
	profileExtractInterval = 1
	programName            = "openshift-tuned"
	tunedProfilesDir       = "/etc/tuned"
	tunedActiveProfileFile = tunedProfilesDir + "/active_profile"
	tunedRecommendDir      = tunedProfilesDir + "/recommend.d"
	tunedRecommendFile     = tunedRecommendDir + "/50-openshift.conf"
	tunedBootcmdlineEnvVar = "TUNED_BOOT_CMDLINE"
	tunedBootcmdlineFile   = tunedProfilesDir + "/bootcmdline"
	openshiftTunedRunDir   = "/run/" + programName
	openshiftTunedPidFile  = openshiftTunedRunDir + "/" + programName + ".pid"
	openshiftTunedSocket   = "/var/lib/tuned/openshift-tuned.sock"
	// With the DefaultControllerRateLimiter, retries will happen at 5ms*2^(retry_n-1)
	// 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s, 5.1s, 10.2s, 20.4s, 41s, 82s
	maxRetries = 15
	// workqueue related constants
	wqKindTuned   = "tuned"
	wqKindProfile = "profile"
)

// Types
type arrayFlags []string

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
		// did node profile change?
		profile bool
		// did the "rendered" tuned object change?
		rendered bool
		// did tuned active_profile/bootcmdline change on the filesystem?
		cfg bool
	}

	tunedCmd  *exec.Cmd       // external command (tuned) being prepared or run
	tunedExit chan bool       // bi-directional channel to signal and register tuned daemon exit
	stopCh    <-chan struct{} // receive-only channel to stop the openshift-tuned controller
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
		kubeconfig: kubeconfig,
		workqueue:  workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		listers:    listers,
		clients:    clients,
		tunedExit:  make(chan bool, 1),
		stopCh:     stopCh,
	}

	return controller, nil
}

// eventProcessor is a long-running function that will continually
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
	klog.V(1).Infof("disabling system tuned...")
	cmd := exec.Command("/usr/bin/systemctl", "disable", "tuned", "--now")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		klog.V(1).Infof("failed to disable system tuned: %s", stderr.String()) // do not use log.Printf(), tuned has its own timestamping
	}
}

// profilesExtract extracts tuned daemon profiles to the daemon configuration directory.
// If the data in the to be extracted recommended profile is different than the current
// recommended profile, the function returns true.
func profilesExtract(profiles []tunedv1.TunedProfile) (bool, error) {
	var (
		change bool
	)
	klog.Infof("extracting tuned profiles")

	recommendedProfile, err := getRecommendedProfile()
	if err != nil {
		return change, err
	}
	for index, profile := range profiles {
		if profile.Name == nil {
			klog.Warningf("profilesExtract(): profile name missing for profile %v", index)
			continue
		}
		if profile.Data == nil {
			klog.Warningf("profilesExtract(): profile data missing for profile %v", index)
			continue
		}
		profileDir := fmt.Sprintf("%s/%s", tunedProfilesDir, *profile.Name)
		profileFile := fmt.Sprintf("%s/%s", profileDir, "tuned.conf")

		if err := mkdir(profileDir); err != nil {
			return change, fmt.Errorf("failed to create tuned profile directory %q: %v", profileDir, err)
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
			klog.Infof("recommended tuned profile %s content %schanged", recommendedProfile, un)
		}

		f, err := os.Create(profileFile)
		if err != nil {
			return change, fmt.Errorf("failed to create tuned profile file %q: %v", profileFile, err)
		}
		defer f.Close()
		if _, err = f.WriteString(*profile.Data); err != nil {
			return change, fmt.Errorf("failed to write tuned profile file %q: %v", profileFile, err)
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
	klog.Infof("written %q to set tuned profile %s", tunedRecommendFile, profileName)
	return nil
}

func tunedCreateCmd() *exec.Cmd {
	return exec.Command("/usr/sbin/tuned", "--no-dbus")
}

func (c *Controller) tunedRun() {
	klog.Infof("starting tuned...")

	defer func() {
		c.tunedExit <- true
	}()

	cmdReader, err := c.tunedCmd.StderrPipe()
	if err != nil {
		klog.Errorf("error creating StderrPipe for tuned: %v", err)
		return
	}

	scanner := bufio.NewScanner(cmdReader)
	go func() {
		for scanner.Scan() {
			fmt.Printf("%s\n", scanner.Text())
		}
	}()

	err = c.tunedCmd.Start()
	if err != nil {
		klog.Errorf("error starting tuned: %v", err)
		return
	}

	err = c.tunedCmd.Wait()
	if err != nil {
		// The command exited with non 0 exit status, e.g. terminated by a signal
		klog.Errorf("error waiting for tuned: %v", err)
		return
	}

	return
}

func (c *Controller) tunedStop(s *sockAccepted) error {
	if c.tunedCmd == nil {
		// Looks like there has been a termination signal prior to starting tuned
		return nil
	}
	if c.tunedCmd.Process != nil {
		klog.V(1).Infof("sending TERM to PID %d", c.tunedCmd.Process.Pid)
		c.tunedCmd.Process.Signal(syscall.SIGTERM)
	} else {
		// This should never happen
		return fmt.Errorf("cannot find the tuned process!")
	}
	// Wait for tuned process to stop -- this will enable node-level tuning rollback
	<-c.tunedExit
	klog.V(1).Infof("tuned process terminated")

	if s != nil {
		// This was a socket-initiated shutdown; indicate a successful settings rollback
		ok := []byte{'o', 'k'}
		_, err := (*s).conn.Write(ok)
		if err != nil {
			return fmt.Errorf("cannot write a response via %q: %v", openshiftTunedSocket, err)
		}
	}

	return nil
}

func (c *Controller) tunedReload() error {
	if c.tunedCmd == nil {
		// Tuned hasn't been started by openshift-tuned, start it
		c.tunedCmd = tunedCreateCmd()
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
		return fmt.Errorf("cannot find the tuned process!")
	}

	return nil
}

// getActiveProfile returns active profile currently in use by the tuned daemon.
// On error, an empty string is returned.
func getActiveProfile() (string, error) {
	var responseString = ""

	f, err := os.Open(tunedActiveProfileFile)
	if err != nil {
		return "", fmt.Errorf("error opening tuned active profile file %s: %v", tunedActiveProfileFile, err)
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
		return "", fmt.Errorf("error opening tuned bootcmdline file %s: %v", tunedBootcmdlineFile, err)
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

func (c *Controller) timedTunedReloader() (err error) {
	var reload bool

	// Check whether reload of tuned is really necessary due to a profile change
	if c.change.profile {
		// Profile changed
		var activeProfile, recommendedProfile string
		if activeProfile, err = getActiveProfile(); err != nil {
			return err
		}
		if recommendedProfile, err = getRecommendedProfile(); err != nil {
			return err
		}
		if activeProfile != recommendedProfile {
			klog.Infof("active profile (%s) != recommended profile (%s)", activeProfile, recommendedProfile)
			recommendedProfileDir := tunedProfilesDir + "/" + recommendedProfile
			if _, err := os.Stat(recommendedProfileDir); os.IsNotExist(err) {
				// Workaround for tuned BZ1774645; do not send SIGHUP to tuned if the profile directory doesn't exist.
				// Log this as an error for easier debugging.  Persistent non-existence of the profile directory very
				// likely indicates a custom user profile which references a profile that was not defined.
				klog.Errorf("tuned profile directory %q does not exist; was %q defined?", recommendedProfileDir, recommendedProfile)
				return nil // retry later
			}
			reload = true
		} else {
			klog.Infof("active and recommended profile (%s) match; profile change will not trigger profile reload", activeProfile)
		}
		c.change.profile = false
	}
	if c.change.rendered {
		// The "rendered" tuned object changed
		c.change.rendered = false
		reload = true
	}
	if c.change.cfg {
		// Tuned active_profile/bootcmldine changed on the filesystem
		c.change.cfg = false
		c.updateTunedProfile()
	}

	if reload {
		err = c.tunedReload()
	}
	return err
}

func getTuned(obj interface{}) (tuned *tunedv1.Tuned, err error) {
	tuned, ok := obj.(*tunedv1.Tuned)
	if !ok {
		return nil, fmt.Errorf("could not convert object to a tuned object: %+v", obj)
	}
	return tuned, nil
}

func getTunedProfile(obj interface{}) (profile *tunedv1.Profile, err error) {
	profile, ok := obj.(*tunedv1.Profile)
	if !ok {
		return nil, fmt.Errorf("could not convert object to a tuned profile object: %+v", obj)
	}

	return profile, nil
}

func getNodeName() string {
	name := os.Getenv("OCP_NODE_NAME")
	if len(name) == 0 {
		// Something is seriously wrong, OCP_NODE_NAME must be defined via tuned DaemonSet
		panic("OCP_NODE_NAME unset or empty")
	}
	return name
}

func (c *Controller) updateTunedProfile() (err error) {
	var bootcmdline string

	if bootcmdline, err = getBootcmdline(); err != nil {
		return nil
	}

	profile, err := c.listers.TunedProfiles.Get(getNodeName())
	if err != nil {
		return fmt.Errorf("failed to get Profile %s: %v", profile.Name, err)
	}

	if profile.Status.Bootcmdline == bootcmdline {
		// Do not update node profile unnecessarily (i.e. when bootcmdline did not change)
		klog.V(2).Infof("updateTunedProfile(): no need to update Profile %s", profile.Name)
		return nil
	}

	profile = profile.DeepCopy() // never update the objects from cache

	profile.Status.Bootcmdline = bootcmdline
	_, err = c.clients.Tuned.TunedV1().Profiles(operandNamespace).Update(context.TODO(), profile, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Profile %s status: %v", profile.Name, err)
	}
	klog.Infof("updated Profile %s with bootcmdline: %s", profile.Name, bootcmdline)

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

			if workqueueKey.kind == wqKindProfile && workqueueKey.name == getNodeName() {
				// When moving this code elsewhere, consider whether it is desirable
				// to disable system tuned on nodes that should not be managed by
				// openshift-tuned.
				disableSystemTuned()
			}

			klog.V(2).Infof("add event to workqueue due to %s (add)", util.ObjectInfo(o))
			c.workqueue.Add(wqKey{kind: workqueueKey.kind, name: accessor.GetName()})
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

	stopCh := make(chan struct{})
	defer close(stopCh)

	trInformer := tunedInformerFactory.Tuned().V1().Tuneds()
	c.listers.TunedResources = trInformer.Lister().Tuneds(operandNamespace)
	trInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindTuned}))

	tpInformer := tunedInformerFactory.Tuned().V1().Profiles()
	c.listers.TunedProfiles = tpInformer.Lister().Profiles(operandNamespace)
	tpInformer.Informer().AddEventHandler(c.informerEventHandler(wqKey{kind: wqKindProfile}))

	tunedInformerFactory.Start(stopCh) // Tuned/Profile

	// Wait for the caches to be synced before starting worker(s)
	klog.V(1).Info("waiting for informer caches to sync")
	ok := cache.WaitForCacheSync(stopCh,
		trInformer.Informer().HasSynced,
		tpInformer.Informer().HasSynced,
	)
	if !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.V(1).Info("starting events processor")
	go wait.Until(c.eventProcessor, time.Second, stopCh)
	klog.Info("started events processor")

	// Create a ticker to extract new profiles and possibly reload tuned;
	// this also rate-limits reloads to a maximum of profileExtractInterval reloads/s
	tickerReload := time.NewTicker(time.Second * time.Duration(profileExtractInterval))
	defer tickerReload.Stop()

	// Watch for filesystem changes on the /etc/tuned/{active_profile,bootcmdline} files
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
			// Termination signal received, stop
			klog.V(2).Infof("changeWatcher done")
			if err := c.tunedStop(nil); err != nil {
				klog.Errorf("%s", err.Error())
			}
			return nil

		case s := <-sockConns:
			if s.err != nil {
				return fmt.Errorf("connection accept error: %v", err)
			}

			buf := make([]byte, len("stop"))
			nr, _ := s.conn.Read(buf)
			data := buf[0:nr]

			if string(data) == "stop" {
				if err := c.tunedStop(&s); err != nil {
					klog.Errorf("%s", err.Error())
				}
				return nil
			}

		case <-c.tunedExit:
			c.tunedCmd = nil // Cmd.Start() cannot be used more than once
			return fmt.Errorf("tuned process exitted")

		case fsEvent := <-wFs.Events:
			klog.V(2).Infof("fsEvent")
			if fsEvent.Op&fsnotify.Write == fsnotify.Write {
				klog.V(1).Infof("write event on: %s", fsEvent.Name)
				c.change.cfg = true
			}

		case err := <-wFs.Errors:
			return fmt.Errorf("error watching filesystem: %v", err)

		case <-tickerReload.C:
			klog.V(2).Infof("tickerReload.C")

			if err := c.timedTunedReloader(); err != nil {
				return err
			}
		}
	}
}

func retryLoop(stopCh <-chan struct{}) (err error) {
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

	c, err := newController(stopCh)
	if err != nil {
		klog.Fatal(err)
	}

	errsTimeStart := time.Now().Unix()
	for {
		err = c.changeWatcher()
		if err == nil {
			break
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
				break
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
	return err
}

func Run(stopCh <-chan struct{}, boolVersion *bool, version string) {
	parseCmdOpts()

	if *boolVersion {
		fmt.Fprintf(os.Stderr, "%s %s\n", programName, version)
		os.Exit(0)
	}

	err := openshiftTunedPidFileWrite()
	if err != nil {
		panic(err.Error())
	}

	err = retryLoop(stopCh)
	if err != nil {
		panic(err.Error())
	}
}
