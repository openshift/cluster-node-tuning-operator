package tuned

import (
	"bufio"         // scanner
	"bytes"         // bytes.Buffer
	"flag"          // command-line options parsing
	"fmt"           // Printf()
	"io/ioutil"     // ioutil.ReadFile()
	"math"          // math.Pow()
	"net"           // net.Conn
	"os"            // os.Exit(), os.Signal, os.Stderr, ...
	"os/exec"       // os.Exec()
	"os/signal"     // signal.Notify()
	"os/user"       // user.Current()
	"path/filepath" // filepath.Join()
	"strconv"       // strconv
	"strings"       // strings.Join()
	"syscall"       // syscall.SIGHUP, ...
	"time"          // time.Second, ...

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	yaml "gopkg.in/yaml.v2"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	tunedclientset "github.com/openshift/cluster-node-tuning-operator/pkg/generated/clientset/versioned"
)

// Types
type arrayFlags []string

type sockAccepted struct {
	conn net.Conn
	err  error
}

type tunedState struct {
	change struct {
		// did profile change?
		profile bool
		// did the "rendered" tuned object change?
		rendered bool
		// did tuned profiles/recommend config change on the filesystem?
		cfg bool
	}
}

// Constants
const (
	operandNamespace       = "openshift-cluster-node-tuning-operator"
	profileExtractInterval = 1
	programName            = "openshift-tuned"
	tunedActiveProfileFile = "/etc/tuned/active_profile"
	tunedProfilesConfigMap = "/var/lib/tuned/profiles-data/tuned-profiles.yaml"
	tunedProfilesDir       = "/etc/tuned"
	tunedRecommendDir      = tunedProfilesDir + "/recommend.d"
	tunedRecommendFile     = tunedRecommendDir + "/" + "50-openshift.conf"
	openshiftTunedRunDir   = "/run/" + programName
	openshiftTunedPidFile  = openshiftTunedRunDir + "/" + programName + ".pid"
	openshiftTunedSocket   = "/var/lib/tuned/openshift-tuned.sock"
)

// Global variables
var (
	done               = make(chan bool, 1)
	tunedExit          = make(chan bool, 1)
	terminationSignals = []os.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}
	cmd                *exec.Cmd
)

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
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <NODE>\n", programName)
		fmt.Fprintf(os.Stderr, "Example: %s b1.lan\n\n", programName)
		fmt.Fprintf(os.Stderr, "Options:\n")

		flag.PrintDefaults()
	}

	flag.Parse()
}

func signalHandler() chan os.Signal {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, terminationSignals...)
	go func() {
		sig := <-sigs
		klog.V(1).Infof("received signal: %v", sig)
		done <- true
	}()
	return sigs
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

// getConfig creates a *rest.Config for talking to a Kubernetes apiserver.
//
// Config precedence
//
// * KUBECONFIG environment variable pointing at a file
// * In-cluster config if running in cluster
// * $HOME/.kube/config if exists
func getConfig() (*rest.Config, error) {
	configFromFlags := func(kubeConfig string) (*rest.Config, error) {
		if _, err := os.Stat(kubeConfig); err != nil {
			return nil, fmt.Errorf("cannot stat kubeconfig %q", kubeConfig)
		}
		return clientcmd.BuildConfigFromFlags("", kubeConfig)
	}

	// If an env variable is specified with the config location, use that
	kubeConfig := os.Getenv("KUBECONFIG")
	if len(kubeConfig) > 0 {
		return configFromFlags(kubeConfig)
	}
	// If no explicit location, try the in-cluster config
	if c, err := rest.InClusterConfig(); err == nil {
		return c, nil
	}
	// If no in-cluster config, try the default location in the user's home directory
	if usr, err := user.Current(); err == nil {
		kubeConfig := filepath.Join(usr.HomeDir, ".kube", "config")
		return configFromFlags(kubeConfig)
	}

	return nil, fmt.Errorf("could not locate a kubeconfig")
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

// This function is for backward-compatibility with older versions of NTO, it will be removed.
func profilesExtractCM() error {
	klog.Infof("extracting tuned profiles from %s", tunedProfilesConfigMap)

	tunedProfilesYaml, err := ioutil.ReadFile(tunedProfilesConfigMap)
	if err != nil {
		// This error is no longer fatal since we support profiles in the "rendered" Tuned object;
		// the file may simply not exist when running the latest NTO
		return nil
	}

	mProfiles := make(map[string]string)

	err = yaml.Unmarshal(tunedProfilesYaml, &mProfiles)
	if err != nil {
		return fmt.Errorf("failed to parse tuned profiles ConfigMap file %q: %v", tunedProfilesConfigMap, err)
	}

	for key, value := range mProfiles {
		profileDir := fmt.Sprintf("%s/%s", tunedProfilesDir, key)
		profileFile := fmt.Sprintf("%s/%s", profileDir, "tuned.conf")

		if err = mkdir(profileDir); err != nil {
			return fmt.Errorf("failed to create tuned profile directory %q: %v", profileDir, err)
		}

		f, err := os.Create(profileFile)
		if err != nil {
			return fmt.Errorf("failed to create tuned profile file %q: %v", profileFile, err)
		}
		defer f.Close()
		if _, err = f.WriteString(value); err != nil {
			return fmt.Errorf("failed to write tuned profile file %q: %v", profileFile, err)
		}
	}
	return nil
}

func profilesExtract(profiles []tunedv1.TunedProfile) error {
	klog.Infof("extracting tuned profiles")

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
			return fmt.Errorf("failed to create tuned profile directory %q: %v", profileDir, err)
		}

		f, err := os.Create(profileFile)
		if err != nil {
			return fmt.Errorf("failed to create tuned profile file %q: %v", profileFile, err)
		}
		defer f.Close()
		if _, err = f.WriteString(*profile.Data); err != nil {
			return fmt.Errorf("failed to write tuned profile file %q: %v", profileFile, err)
		}
	}

	return nil
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
	if _, err = f.WriteString(fmt.Sprintf("[%s]\n%s=.*\n", profileName, tunedRecommendFile)); err != nil {
		return fmt.Errorf("failed to write file %q: %v", tunedRecommendFile, err)
	}
	return nil
}

func tunedCreateCmd() *exec.Cmd {
	return exec.Command("/usr/sbin/tuned", "--no-dbus")
}

func tunedRun() {
	klog.Infof("starting tuned...")

	defer func() {
		tunedExit <- true
	}()

	cmdReader, err := cmd.StderrPipe()
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

	err = cmd.Start()
	if err != nil {
		klog.Errorf("error starting tuned: %v", err)
		return
	}

	err = cmd.Wait()
	if err != nil {
		// The command exited with non 0 exit status, e.g. terminated by a signal
		klog.Errorf("error waiting for tuned: %v", err)
		return
	}

	return
}

func tunedStop(s *sockAccepted) error {
	if cmd == nil {
		// Looks like there has been a termination signal prior to starting tuned
		return nil
	}
	if cmd.Process != nil {
		klog.V(1).Infof("sending TERM to PID %d", cmd.Process.Pid)
		cmd.Process.Signal(syscall.SIGTERM)
	} else {
		// This should never happen
		return fmt.Errorf("cannot find the tuned process!")
	}
	// Wait for tuned process to stop -- this will enable node-level tuning rollback
	<-tunedExit
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

func tunedReload() error {
	if cmd == nil {
		// Tuned hasn't been started by openshift-tuned, start it
		cmd = tunedCreateCmd()
		go tunedRun()
		return nil
	}

	klog.Infof("reloading tuned...")

	if cmd.Process != nil {
		klog.Infof("sending HUP to PID %d", cmd.Process.Pid)
		err := cmd.Process.Signal(syscall.SIGHUP)
		if err != nil {
			return fmt.Errorf("error sending SIGHUP to PID %d: %v\n", cmd.Process.Pid, err)
		}
	} else {
		// This should never happen
		return fmt.Errorf("cannot find the tuned process!")
	}

	return nil
}

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

func timedTunedReloader(tuned *tunedState) (err error) {
	var reload bool

	// Check whether reload of tuned is really necessary due to a profile change
	if tuned.change.profile {
		// Profile changed
		var activeProfile, recommendedProfile string
		if activeProfile, err = getActiveProfile(); err != nil {
			return err
		}
		if recommendedProfile, err = getRecommendedProfile(); err != nil {
			return err
		}
		if activeProfile != recommendedProfile {
			klog.V(1).Infof("active profile (%s) != recommended profile (%s)", activeProfile, recommendedProfile)
			recommendedProfileDir := tunedProfilesDir + "/" + recommendedProfile
			if _, err := os.Stat(recommendedProfileDir); os.IsNotExist(err) {
				// Workaround for tuned BZ1774645; do not send SIGHUP to tuned if the profile directory doesn't exist
				klog.V(1).Infof("tuned profile directory %q does not exist", recommendedProfileDir)
				return nil // retry later on a filesystem event
			}
			reload = true
		} else {
			klog.V(1).Infof("active and recommended profile (%s) match; profile change will not trigger profile reload", activeProfile)
		}
		tuned.change.profile = false
	}
	if tuned.change.rendered {
		// The "rendered" tuned object changed
		klog.V(1).Infof("tuned daemon profiles changed, forcing tuned daemon reload")
		tuned.change.rendered = false
		reload = true
	}

	if reload {
		err = tunedReload()
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

func profileEventHandler(tuned *tunedState) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			p, err := getTunedProfile(obj)
			if err != nil {
				klog.Errorf("%s", err.Error())
				return
			}
			klog.V(1).Infof("profile %q added, tuned profile requested: %s", p.ObjectMeta.Name, p.Spec.Config.TunedProfile)
			// When moving this call elsewhere, remember it is undesirable to disable system tuned
			// on nodes that should not be managed by openshift-tuned
			disableSystemTuned()
			err = tunedRecommendFileWrite(p.Spec.Config.TunedProfile)
			if err != nil {
				klog.Errorf("%s", err.Error())
				return
			}
			tuned.change.profile = true
		},
		UpdateFunc: func(objOld, objNew interface{}) {
			pNew, err := getTunedProfile(objNew)
			if err != nil {
				klog.Errorf("%s", err.Error())
				return
			}
			klog.V(1).Infof("profile %q changed, tuned profile requested: %s", pNew.ObjectMeta.Name, pNew.Spec.Config.TunedProfile)
			err = tunedRecommendFileWrite(pNew.Spec.Config.TunedProfile)
			if err != nil {
				klog.Errorf("%s", err.Error())
				return
			}
			tuned.change.profile = true
		},
		DeleteFunc: func(obj interface{}) {
			p, err := getTunedProfile(obj)
			if err != nil {
				klog.Errorf("%s", err.Error())
				return
			}
			klog.V(1).Infof("profile %q deleted, keeping the old tuned profile: %s", p.ObjectMeta.Name, p.Spec.Config.TunedProfile)
		},
	}
}

func tunedEventHandler(tuned *tunedState) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			t, err := getTuned(obj)
			if err != nil {
				klog.Errorf("%s", err.Error())
				return
			}
			klog.V(1).Infof("tuned %q added", t.ObjectMeta.Name)
			err = profilesExtract(t.Spec.Profile)
			if err != nil {
				klog.Errorf("%s", err.Error())
				return
			}
			tuned.change.rendered = true
		},
		UpdateFunc: func(objOld, objNew interface{}) {
			tNew, err := getTuned(objNew)
			if err != nil {
				klog.Errorf("%s", err.Error())
				return
			}
			klog.V(1).Infof("tuned %q changed", tNew.ObjectMeta.Name)
			err = profilesExtract(tNew.Spec.Profile)
			if err != nil {
				klog.Errorf("%s", err.Error())
				return
			}
			tuned.change.rendered = true
		},
		DeleteFunc: func(obj interface{}) {
			t, err := getTuned(obj)
			if err != nil {
				klog.Errorf("%s", err.Error())
				return
			}
			klog.V(1).Infof("tuned %q deleted, keeping the old tuned profile", t.ObjectMeta.Name)
		},
	}
}

func changeWatcher() (err error) {
	var (
		tuned     tunedState
		lStop     bool
		nodeName  string          = flag.Args()[0]
		profileFS fields.Selector = fields.SelectorFromSet(fields.Set{"metadata.name": nodeName})
		tunedFS   fields.Selector = fields.SelectorFromSet(fields.Set{"metadata.name": tunedv1.TunedRenderedResourceName})
	)

	kubeConfig, err := getConfig()
	if err != nil {
		return err
	}

	cs, err := tunedclientset.NewForConfig(kubeConfig)
	if err != nil {
		return err
	}

	// Perform an initial list and start a watch on Profiles in operand namespace
	profileLW := cache.NewListWatchFromClient(cs.TunedV1().RESTClient(), "Profiles", operandNamespace, profileFS)
	tunedLW := cache.NewListWatchFromClient(cs.TunedV1().RESTClient(), "Tuneds", operandNamespace, tunedFS)

	stop := make(chan struct{})
	defer close(stop)

	siProfile := cache.NewSharedInformer(profileLW, &tunedv1.Profile{}, 0)
	siProfile.AddEventHandler(profileEventHandler(&tuned))
	go siProfile.Run(stop)

	siTuned := cache.NewSharedInformer(tunedLW, &tunedv1.Tuned{}, 0)
	siTuned.AddEventHandler(tunedEventHandler(&tuned))
	go siTuned.Run(stop)

	// Create a ticker to extract new profiles and possibly reload tuned;
	// this also rate-limits reloads to a maximum of profileExtractInterval reloads/s
	tickerReload := time.NewTicker(time.Second * time.Duration(profileExtractInterval))
	defer tickerReload.Stop()

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

	for {
		select {
		case <-done:
			// Termination signal received, stop
			klog.V(2).Infof("changeWatcher done")
			if err := tunedStop(nil); err != nil {
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
				if err := tunedStop(&s); err != nil {
					klog.Errorf("%s", err.Error())
				}
				return nil
			}

		case <-tunedExit:
			cmd = nil // cmd.Start() cannot be used more than once
			return fmt.Errorf("tuned process exitted")

		case <-tickerReload.C:
			klog.V(2).Infof("tickerReload.C")
			if err := timedTunedReloader(&tuned); err != nil {
				return err
			}
		}
	}
}

func retryLoop() (err error) {
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
	errsTimeStart := time.Now().Unix()
	for {
		err = changeWatcher()
		if err == nil {
			break
		}

		select {
		case <-done:
			return err
		default:
		}

		klog.Errorf("%s", err.Error())
		sleepRetry *= 2
		klog.V(1).Infof("increased retry period to %d", sleepRetry)
		if errs++; errs >= errsMax {
			now := time.Now().Unix()
			if (now - errsTimeStart) <= errsMaxWithinSeconds {
				klog.Errorf("seen %d errors in %d seconds (limit was %d), terminating...", errs, now-errsTimeStart, errsMaxWithinSeconds)
				break
			}
			errs = 0
			sleepRetry = sleepRetryInit
			errsTimeStart = time.Now().Unix()
			klog.V(1).Infof("initialized retry period to %d", sleepRetry)
		}

		select {
		case <-done:
			return nil
		case <-time.After(time.Second * time.Duration(sleepRetry)):
			continue
		}
	}
	return err
}

func Run(boolVersion *bool, version string) {
	parseCmdOpts()

	if *boolVersion {
		fmt.Fprintf(os.Stderr, "%s %s\n", programName, version)
		os.Exit(0)
	}

	if len(flag.Args()) != 1 {
		flag.Usage()
		os.Exit(1)
	}

	err := openshiftTunedPidFileWrite()
	if err != nil {
		panic(err.Error())
	}

	sigs := signalHandler()
	err = retryLoop()
	signal.Stop(sigs)
	if err != nil {
		panic(err.Error())
	}
}
