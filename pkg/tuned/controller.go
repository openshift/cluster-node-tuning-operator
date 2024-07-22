package tuned

import (
	"bufio"   // scanner
	"bytes"   // bytes.Buffer
	"context" // context.TODO()
	"crypto/sha256"
	"encoding/hex"
	"errors"  // errors.Is()
	"fmt"     // Printf()
	"math"    // math.Pow()
	"os"      // os.Exit(), os.Stderr, ...
	"os/exec" // os.Exec()
	"path/filepath"
	"sort"
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

// Constants used for instantiating Profile status conditions;
// they will be set to 2^0, 2^1, 2^2, ..., 2^n
const (
	scApplied Bits = 1 << iota
	scWarn
	scError
	scSysctlOverride
	scReloading // reloading is true during the TuneD daemon reload.
	scDeferred
	scUnknown
)

// Constants used for controlling TuneD;
// they will be set to 2^0, 2^1, 2^2, ..., 2^n
const (
	// Should we reload (vs. full restart) TuneD?
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
	tunedEtcDir            = "/etc/tuned"
	tunedProfilesDirCustom = ocpTunedHome + "/profiles"
	tunedProfilesDirSystem = "/usr/lib/tuned"
	tunedConfFile          = "tuned.conf"
	tunedMainConfPath      = tunedEtcDir + "/tuned-main.conf"
	tunedActiveProfileFile = tunedEtcDir + "/active_profile"
	tunedRecommendDir      = tunedEtcDir + "/recommend.d"
	tunedRecommendFile     = tunedRecommendDir + "/50-openshift.conf"
	tunedBootcmdlineEnvVar = "TUNED_BOOT_CMDLINE"
	tunedBootcmdlineFile   = tunedEtcDir + "/bootcmdline"
	// A couple of seconds should be more than enough for TuneD daemon to gracefully stop;
	// be generous and give it 10s.
	tunedGracefulExitWait = time.Second * time.Duration(10)
	ocpTunedHome          = "/var/lib/ocp-tuned"
	ocpTunedRunDir        = "/run/" + programName
	ocpTunedProvider      = ocpTunedHome + "/provider"
	// With the less aggressive rate limiter, retries will happen at 100ms*2^(retry_n-1):
	// 100ms, 200ms, 400ms, 800ms, 1.6s, 3.2s, 6.4s, 12.8s, 25.6s, 51.2s, 102.4s, 3.4m, 6.8m, 13.7m, 27.3m
	maxRetries = 15
	// workqueue related constants
	wqKindDaemon  = "daemon"
	wqKindProfile = "profile"

	ocpTunedImageEnv           = ocpTunedHome + "/image.env"
	tunedProfilesDirCustomHost = ocpTunedHome + "/profiles"
	tunedRecommendDirHost      = ocpTunedHome + "/recommend.d"

	// How do we detect a reboot? The NTO operand owns and uses two separate files to track deferred updates.
	// 1. /var/lib/... - persistent storage which will survive across reboots. Contains the actual data.
	// 2. /run/..      - ephemeral file on tmpfs. Lost on reboot. Since this file is going to be wiped out
	//                   automatically and implicitly on reboot, if it is missing we assume a reboot.
	// this means the admin can tamper the node state and fake a node restart by deleting this file
	// and restarting the tuned daemon.
	tunedDeferredUpdateEphemeralFilePath  = ocpTunedRunDir + "/pending_profile"
	tunedDeferredUpdatePersistentFilePath = ocpTunedHome + "/pending_profile"
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
	// profileFingerprintUnpacked is the fingerprint of the profile unpacked on the node.
	// Relevant in the startup flow with deferred updates.
	profileFingerprintUnpacked string
	// profileFingerprintEffective is the fingerprint of the profile effective on the node.
	// Relevant in the startup flow with deferred updates.
	profileFingerprintEffective string
}

type Change struct {
	// Did the node Profile k8s object change?
	profile bool
	// Do we need to update Tuned Profile status?
	profileStatus bool

	// Is this Change caused by a TuneD reload?
	tunedReload bool

	// Is this Change caused by a node restart?
	nodeRestart bool

	// The following keys are set when profile == true.
	// Was debugging set in Profile k8s object?
	debug bool
	// Cloud Provider as detected by the operator.
	provider string
	// Should we turn the reapply_sysctl TuneD option on in tuned-main.conf file?
	reapplySysctl bool
	// The current recommended profile as calculated by the operator.
	recommendedProfile string

	// Is the current Change triggered by an object with the deferred annotation?
	deferred bool
	// Text to convey in status message, if present.
	message string
}

func (ch Change) String() string {
	var items []string
	if ch.profile {
		items = append(items, "profile:true")
	}
	if ch.profileStatus {
		items = append(items, "profileStatus:true")
	}
	if ch.tunedReload {
		items = append(items, "tunedReload:true")
	}
	if ch.nodeRestart {
		items = append(items, "nodeRestart:true")
	}
	if ch.debug {
		items = append(items, "debug:true")
	}
	if ch.provider != "" {
		items = append(items, fmt.Sprintf("provider:%q", ch.provider))
	}
	if ch.reapplySysctl {
		items = append(items, "reapplySysctl:true")
	}
	if ch.recommendedProfile != "" {
		items = append(items, fmt.Sprintf("recommendedProfile:%q", ch.recommendedProfile))
	}
	if ch.deferred {
		items = append(items, "deferred:true")
	}
	if ch.message != "" {
		items = append(items, fmt.Sprintf("message:%q", ch.message))
	}
	return "tuned.Change{" + strings.Join(items, ", ") + "}"
}

type Controller struct {
	kubeclient kubernetes.Interface

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens.
	wqKube  workqueue.RateLimitingInterface
	wqTuneD workqueue.RateLimitingInterface

	listers *ntoclient.Listers
	clients *ntoclient.Clients

	daemon Daemon

	nodeName string

	tunedCmd     *exec.Cmd       // external command (tuned) being prepared or run
	tunedExit    chan bool       // bi-directional channel to signal and register TuneD daemon exit
	stopCh       <-chan struct{} // receive-only channel to stop the openshift-tuned controller
	changeCh     chan Change     // bi-directional channel to wake-up the main thread to process accrued changes
	changeChRet  chan bool       // bi-directional channel to announce success/failure of change processing
	tunedMainCfg *ini.File       // global TuneD configuration as defined in tuned-main.conf

	pendingChange *Change // pending deferred change to be applied on node restart (if any)
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

func newController(nodeName string, stopCh <-chan struct{}) (*Controller, error) {
	kubeconfig, err := ntoclient.GetConfig()
	if err != nil {
		return nil, err
	}
	kubeclient, err := getKubeClient()
	if err != nil {
		return nil, err
	}

	tunedclient, err := tunedset.NewForConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	listers := &ntoclient.Listers{}
	clients := &ntoclient.Clients{
		Tuned: tunedclient,
	}

	controller := &Controller{
		nodeName:    nodeName,
		kubeclient:  kubeclient,
		listers:     listers,
		clients:     clients,
		tunedExit:   make(chan bool),
		stopCh:      stopCh,
		changeCh:    make(chan Change),
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
	case key.kind == wqKindProfile:
		var change Change
		if key.name != c.nodeName {
			return nil
		}
		klog.V(2).Infof("sync(): Profile %s", key.name)

		profile, err := c.listers.TunedProfiles.Get(c.nodeName)
		if err != nil {
			return fmt.Errorf("failed to get Profile %s: %v", key.name, err)
		}

		change.profile = true
		change.provider = profile.Spec.Config.ProviderName
		change.recommendedProfile = profile.Spec.Config.TunedProfile
		change.debug = profile.Spec.Config.Debug
		change.reapplySysctl = true
		if profile.Spec.Config.TuneDConfig.ReapplySysctl != nil {
			change.reapplySysctl = *profile.Spec.Config.TuneDConfig.ReapplySysctl
		}
		change.deferred = util.HasDeferredUpdateAnnotation(profile.Annotations)
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

type ExtractedProfiles struct {
	// True if the data in the to-be-extracted recommended profile or the profiles being
	// included from the current recommended profile have changed.
	Changed bool
	// If the data changed, the fingerprint of the new profile, or "" otherwise.
	Fingerprint string
	// A map with successfully extracted TuneD profile names.
	Names map[string]bool
	// A map with names of TuneD profiles the current TuneD recommended profile depends on.
	Dependencies map[string]bool
}

// ProfilesExtract extracts TuneD daemon profiles to tunedProfilesDirCustom directory.
// Returns:
//   - ExtractedProfiles with the details of the operation performed
//   - Error if any or nil.
func ProfilesExtract(profiles []tunedv1.TunedProfile, recommendedProfile string) (ExtractedProfiles, error) {
	klog.Infof("profilesExtract(): extracting %d TuneD profiles (recommended=%s)", len(profiles), recommendedProfile)
	// Get a list of TuneD profiles names the recommended profile depends on.
	deps := profileDepends(recommendedProfile)
	// Add the recommended profile itself.
	deps[recommendedProfile] = true
	klog.V(2).Infof("profilesExtract(): profile deps: %#v", deps)
	return profilesExtractPathWithDeps(tunedProfilesDirCustom, profiles, recommendedProfile, deps)
}

// profilesExtractPathWithDeps is like ProfilesExtract but takes explicit profiles root dir path and
// explicit dependencies, so it's easier to test. To be used only internally.
func profilesExtractPathWithDeps(profilesRootDir string, profiles []tunedv1.TunedProfile, recommendedProfile string, recommendedProfileDeps map[string]bool) (ExtractedProfiles, error) {
	var (
		change    bool            = false
		extracted map[string]bool = map[string]bool{} // TuneD profile names present in TuneD CR and successfully extracted to tunedProfilesDirCustom
	)

	for index, profile := range profiles {
		if profile.Name == nil {
			klog.Warningf("profilesExtract(): profile name missing for Profile %v", index)
			continue
		}
		if profile.Data == nil {
			klog.Warningf("profilesExtract(): profile data missing for Profile %v", index)
			continue
		}
		profileDir := filepath.Join(profilesRootDir, *profile.Name)
		profileFile := filepath.Join(profileDir, tunedConfFile)

		if err := os.MkdirAll(profileDir, os.ModePerm); err != nil {
			return ExtractedProfiles{
				Changed:      change,
				Names:        extracted,
				Dependencies: recommendedProfileDeps,
			}, fmt.Errorf("failed to create TuneD profile directory %q: %v", profileDir, err)
		}

		if recommendedProfileDeps[*profile.Name] {
			// Recommended profile (dependency) name matches profile name of the profile
			// currently being extracted, compare their content.
			var un string
			change = change || !profilesEqual(profileFile, *profile.Data)
			if !change {
				un = "un"
			}
			klog.Infof("profilesExtract(): recommended TuneD profile %s content %schanged [%s]", recommendedProfile, un, *profile.Name)
		}

		err := os.WriteFile(profileFile, []byte(*profile.Data), 0644)
		if err != nil {
			return ExtractedProfiles{
				Changed:      change,
				Names:        extracted,
				Dependencies: recommendedProfileDeps,
			}, fmt.Errorf("failed to write TuneD profile file %q: %v", profileFile, err)
		}
		extracted[*profile.Name] = true
		klog.V(2).Infof("profilesExtract(): extracted profile %q to %q (%d bytes)", *profile.Name, profileFile, len(*profile.Data))
	}

	profilesFP := profilesFingerprint(profiles, recommendedProfile)
	klog.Infof("profilesExtract(): fingerprint of extracted profiles: %q", profilesFP)
	return ExtractedProfiles{
		Changed:      change,
		Fingerprint:  profilesFP,
		Names:        extracted,
		Dependencies: recommendedProfileDeps,
	}, nil
}

// profilesRepackPath reconstructs the TunedProfile object from the data unpacked on the node
// by earlier operations of the operand code. Takes the paths of the recommend file and of
// the profiles root directory. For testability, the production code always uses the same
// hardcoded path (see for example RunInCluster). Returns the reconstructed TunedProfiles,
// the name of the recommended profile, error if any. If the returned error is not nil,
// the other return values are not significant and should be ignored.
func profilesRepackPath(recommendFilePath, profilesRootDir string) ([]tunedv1.TunedProfile, string, error) {
	recommendedProfile, err := TunedRecommendFileReadPath(recommendFilePath)
	if err != nil {
		return nil, "", err
	}
	klog.V(1).Infof("profilesRepack(): recovered recommended profile: %q", recommendedProfile)

	dents, err := os.ReadDir(profilesRootDir)
	if err != nil {
		return nil, "", err
	}
	var profiles []tunedv1.TunedProfile
	for _, dent := range dents {
		profileDir := filepath.Join(profilesRootDir, dent.Name())
		if !dent.IsDir() {
			klog.V(2).Infof("profilesRepack(): skipped entry %q: not a directory", profileDir)
			continue
		}
		profilePath := filepath.Join(profileDir, tunedConfFile)
		profileBytes, err := os.ReadFile(profilePath)
		if err != nil {
			return profiles, recommendedProfile, err
		}
		profileName := dent.Name()
		profileData := string(profileBytes)
		profiles = append(profiles, tunedv1.TunedProfile{
			Name: &profileName,
			Data: &profileData,
		})
		klog.V(2).Infof("profilesRepack(): recovered profile: %q from %q (%d bytes)", profileName, profilePath, len(profileBytes))
	}

	klog.V(2).Infof("profilesRepack(): recovered %d profiles", len(profiles))
	return profiles, recommendedProfile, nil
}

// profilesSync extracts TuneD daemon profiles to the daemon configuration directory
// and removes any TuneD profiles from <tunedProfilesDirCustom>/<profile>/ once the same TuneD
// <profile> is no longer defined in the 'profiles' slice.
// Returns:
//   - True if the data in the to-be-extracted recommended profile or the profiles being
//     included from the current recommended profile have changed.
//   - The synced profile fingerprint.
//   - Error if any or nil.
func profilesSync(profiles []tunedv1.TunedProfile, recommendedProfile string) (bool, string, error) {
	extracted, err := ProfilesExtract(profiles, recommendedProfile)
	if err != nil {
		return extracted.Changed, "", err
	}

	dirEntries, err := os.ReadDir(tunedProfilesDirCustom)
	if err != nil {
		return extracted.Changed, "", err
	}

	changed := extracted.Changed // shortcut, but we also modify before
	// Deal with TuneD profiles absent from Tuned CRs, but still present in <tunedProfilesDirCustom>/<profile>/
	for _, dirEntry := range dirEntries {
		profile := dirEntry.Name()
		if !dirEntry.IsDir() {
			// There shouldn't be anything but directories in <tunedProfilesDirCustom>, but if there is, skip it.
			continue
		}

		if len(profile) == 0 {
			// This should never happen, but if it does, do not wipe the entire tunedProfilesDirCustom directory.
			continue
		}

		if extracted.Names[profile] {
			// Do not delete the freshly extracted profiles.  These are the profiles present in the Profile CR.
			continue
		}
		// This TuneD profile does not exist in the Profile CR, delete it.
		profileDir := fmt.Sprintf("%s/%s", tunedProfilesDirCustom, profile)
		err := os.RemoveAll(profileDir)
		if err != nil {
			return changed, "", fmt.Errorf("failed to remove %q: %v", profileDir, err)
		}
		klog.Infof("profilesSync(): removed TuneD profile %q", profileDir)

		if extracted.Dependencies[profile] {
			// This TuneD profile does not exist in the Profile CR, but the recommended profile depends on it.
			// Trigger a change to report a configuration issue -- we depend on a profile that does not exist.
			changed = true
		}
	}

	return changed, extracted.Fingerprint, nil
}

// filterAndSortProfiles returns a slice of valid (non-nil name, non-nil data) profiles
// from the given slice, and the returned slice have all the valid profiles sorted by name.
func filterAndSortProfiles(profiles []tunedv1.TunedProfile) []tunedv1.TunedProfile {
	profs := make([]tunedv1.TunedProfile, 0, len(profiles))
	for _, prof := range profiles {
		if prof.Name == nil {
			continue
		}
		if prof.Data == nil {
			continue
		}
		profs = append(profs, prof)
	}
	sort.Slice(profs, func(i, j int) bool { return *profs[i].Name < *profs[j].Name })
	return profs
}

// profilesFingerprint returns a hash of `recommendedProfile` name joined with the data sections of all TuneD profiles in the `profiles` slice.
func profilesFingerprint(profiles []tunedv1.TunedProfile, recommendedProfile string) string {
	profiles = filterAndSortProfiles(profiles)
	h := sha256.New()
	h.Write([]byte(recommendedProfile))
	for _, prof := range profiles {
		h.Write([]byte(*prof.Data))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// providerExtract extracts Cloud Provider name into ocpTunedProvider file.
func providerExtract(provider string) error {
	klog.Infof("providerExtract(): extracting cloud provider name to %v", ocpTunedProvider)
	if err := os.WriteFile(ocpTunedProvider, []byte(provider), 0o644); err != nil {
		return fmt.Errorf("failed to write cloud provider name file %q: %v", ocpTunedProvider, err)
	}
	return nil
}

// Read the Cloud Provider name from ocpTunedProvider file and
// extract/write 'provider' only if the file does not exist or it does not
// match 'provider'.  Returns indication whether the 'provider' changed and
// an error if any.
func providerSync(provider string) (bool, error) {
	providerCurrent, err := os.ReadFile(ocpTunedProvider)
	if err != nil {
		if os.IsNotExist(err) {
			return len(provider) > 0, providerExtract(provider)
		}
		return false, err
	}

	if provider == string(providerCurrent) {
		return false, nil
	}

	return true, providerExtract(provider)
}

// switchTunedHome changes "native" container's home directory as defined by the
// Containerfile to the container's home directory on the host itself.
func switchTunedHome() error {
	const (
		ocpTunedHomeHost = "/host" + ocpTunedHome
	)

	// Create the container's home directory on the host.
	if err := os.MkdirAll(ocpTunedHomeHost, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create directory %q: %v", ocpTunedHomeHost, err)
	}

	// Delete the container's home directory.
	if err := util.Delete(ocpTunedHome); err != nil {
		return fmt.Errorf("failed to delete: %q: %v", ocpTunedHome, err)
	}

	if err := util.Symlink(ocpTunedHomeHost, ocpTunedHome); err != nil {
		return fmt.Errorf("failed to link %q -> %q: %v", ocpTunedHome, ocpTunedHomeHost, err)
	}

	err := os.Chdir(ocpTunedHome)
	if err != nil {
		return err
	}

	return nil
}

func prepareOpenShiftTunedDir() error {
	// Create the following directories unless they exist.
	dirs := []string{
		tunedRecommendDirHost,
		tunedProfilesDirCustomHost,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create directory %q: %v", d, err)
		}
	}

	// Create the following symbolic links.
	links := map[string]string{
		tunedRecommendDirHost: tunedRecommendDir,
	}
	for target, source := range links {
		if err := util.Symlink(target, source); err != nil {
			return fmt.Errorf("failed to link %q -> %q: %v", source, target, err)
		}
	}

	return nil
}

func writeOpenShiftTunedImageEnv() error {
	klog.Infof("writing %v", ocpTunedImageEnv)

	content := fmt.Sprintf("NTO_IMAGE=%s\n", os.Getenv("CLUSTER_NODE_TUNED_IMAGE"))
	err := os.WriteFile(ocpTunedImageEnv, []byte(content), 0644)
	if err != nil {
		return fmt.Errorf("failed to write file %q: %v", ocpTunedImageEnv, err)
	}

	return nil
}

func TunedRecommendFileWritePath(recommendFilePath, profileName string) error {
	rfDir := filepath.Dir(recommendFilePath)
	klog.V(2).Infof("tunedRecommendFileWrite(): %s %s", profileName, rfDir)
	if err := os.MkdirAll(rfDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create directory %q: %v", rfDir, err)
	}
	data := []byte(fmt.Sprintf("[%s]\n", profileName))
	if err := os.WriteFile(recommendFilePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file %q: %v", recommendFilePath, err)
	}
	klog.Infof("tunedRecommendFileWrite(): written %q to set TuneD profile %s", recommendFilePath, profileName)
	return nil
}

func TunedRecommendFileReadPath(recommendFilePath string) (string, error) {
	data, err := os.ReadFile(recommendFilePath)
	if err != nil {
		return "", err
	}
	recommended := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(string(data)), "["), "]")
	klog.Infof("tunedRecommendFileRead(): read %q from %q", recommended, tunedRecommendFile)
	return recommended, nil
}

func TunedRecommendFileWrite(profileName string) error {
	return TunedRecommendFileWritePath(tunedRecommendFile, profileName)
}

func TunedRecommendFileRead() (string, error) {
	return TunedRecommendFileReadPath(tunedRecommendFile)
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
	command, args := TunedCreateCmdline((c.daemon.restart & ctrlDebug) != 0)

	return exec.Command(command, args...)
}

func (c *Controller) tunedRun() {
	klog.Infof("starting tuned...")

	defer func() {
		close(c.tunedExit)
	}()

	c.tunedExit = make(chan bool) // Once tunedStop() terminates, the tunedExit channel is closed!

	onDaemonReload := func() {
		// Notify the event processor that the TuneD daemon finished reloading and that we might need to update Profile status.
		c.wqTuneD.Add(wqKeyTuned{kind: wqKindDaemon, change: Change{
			profileStatus: true,
			tunedReload:   true,
		}})
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
			if errors.Is(err, os.ErrProcessDone) {
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
			if errors.Is(err, os.ErrProcessDone) {
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
			if errors.Is(err, os.ErrProcessDone) {
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

// changeSyncerPostReloadOrRestart synchronizes the daemon status after a node restart or a TuneD reload.
// The deferred updates are meant to be applied only after a full node restart.
// However, after a TuneD reload caused by a immediate update, we need to update the internal state
// pertaining to the effective profile applied on a node.
func (c *Controller) changeSyncerPostReloadOrRestart(change Change) {
	klog.V(2).Infof("changeSyncerPostReloadOrRestart(%s)", change.String())
	defer klog.V(2).Infof("changeSyncerPostReloadOrRestart(%s) done", change.String())

	if !change.nodeRestart && !change.tunedReload {
		return // nothing to do
	}

	profiles, recommended, err := profilesRepackPath(tunedRecommendFile, tunedProfilesDirCustom)
	if err != nil {
		// keep going, immediate updates are expected to work as usual
		// and we expect the vast majority of updates to be immediate anyway
		klog.Errorf("error repacking the profile: %v", err)
		klog.Infof("deferred updates likely broken")
	}

	profileFP := profilesFingerprint(profiles, recommended)
	klog.V(2).Infof("changeSyncerPostReloadOrRestart(): current effective profile fingerprint %q -> %q", c.daemon.profileFingerprintEffective, profileFP)

	c.daemon.profileFingerprintEffective = profileFP
	c.daemon.status &= ^scDeferred // force clear even if it was never set.
}

func (c *Controller) changeSyncerProfileStatus(change Change) (synced bool) {
	klog.V(2).Infof("changeSyncerProfileStatus(%s)", change.String())
	defer klog.V(2).Infof("changeSyncerProfileStatus(%s) done", change.String())

	if !change.profileStatus {
		return true
	}

	// One or both of the following happened:
	// 1) tunedBootcmdlineFile changed on the filesystem.  This is very likely the result of
	//    applying a TuneD profile by the TuneD daemon.  Make sure the node Profile k8s object
	//    is in sync with tunedBootcmdlineFile so the operator can take an appropriate action.
	// 2) TuneD daemon was reloaded.  Make sure the node Profile k8s object is in sync with
	//    the active profile, e.g. the Profile indicates the presence of the stall daemon on
	//    the host if requested by the current active profile.
	if err := c.updateTunedProfile(change); err != nil {
		klog.Error(err.Error())
		return false // retry later
	}
	return true
}

// changeSyncerTuneD synchronizes k8s objects to disk, compares them with
// current TuneD configuration and signals TuneD process reload or restart.
func (c *Controller) changeSyncerTuneD(change Change) (synced bool, err error) {
	var restart bool
	var reload bool
	var cfgUpdated bool
	var changeRecommend bool

	restart = change.nodeRestart
	reload = change.nodeRestart

	klog.V(2).Infof("changeSyncerTuneD(%s) restart=%v reload=%v", change.String(), restart, reload)
	defer klog.V(2).Infof("changeSyncerTuneD(%s) done updated=%v restart=%v reload=%v", change.String(), cfgUpdated, restart, reload)

	if (c.daemon.status & scReloading) != 0 {
		// This should not be necessary, but keep this here as a reminder.
		// We should not manipulate TuneD configuration files in any way while the daemon is reloading/restarting.
		return false, fmt.Errorf("changeSyncerTuneD(): called while the TuneD daemon was reloading")
	}

	// Check whether reload of the TuneD daemon is really necessary due to a Profile change.
	if change.profile {
		changeProvider, err := providerSync(change.provider)
		if err != nil {
			return false, err
		}
		reload = reload || changeProvider

		if (c.daemon.recommendedProfile != change.recommendedProfile) || change.nodeRestart {
			if err = TunedRecommendFileWrite(change.recommendedProfile); err != nil {
				return false, err
			}
			klog.V(1).Infof("recommended TuneD profile changed from %q to %q [deferred=%v nodeRestart=%v]", c.daemon.recommendedProfile, change.recommendedProfile, change.deferred, change.nodeRestart)
			// Cache the value written to tunedRecommendFile.
			c.daemon.recommendedProfile = change.recommendedProfile
			reload = true
		} else if !change.deferred && (c.daemon.status&scDeferred != 0) {
			klog.V(1).Infof("detected deferred update changed to immediate after object update")
			reload = true
		} else {
			klog.V(1).Infof("recommended profile (%s) matches current configuration", c.daemon.recommendedProfile)
			// We do not need to reload the TuneD daemon, however, someone may have tampered with the k8s Profile status for this node.
			// Make sure its status is up-to-date.
			if err = c.updateTunedProfile(change); err != nil {
				klog.Error(err.Error())
				return false, nil // retry later
			}
		}

		profile, err := c.listers.TunedProfiles.Get(c.nodeName)
		if err != nil {
			return false, fmt.Errorf("failed to get Profile %s: %v", c.nodeName, err)
		}

		changeProfiles, profilesFP, err := profilesSync(profile.Spec.Profile, c.daemon.recommendedProfile)
		if err != nil {
			return false, err
		}
		if changeProfiles || changeRecommend {
			if c.daemon.profileFingerprintUnpacked != profilesFP {
				klog.V(2).Infof("current unpacked profile fingerprint %q -> %q", c.daemon.profileFingerprintUnpacked, profilesFP)
				c.daemon.profileFingerprintUnpacked = profilesFP
			}
			reload = true
		}

		// Does the current TuneD process have debugging turned on?
		debug := (c.daemon.restart & ctrlDebug) != 0
		if debug != change.debug {
			// A complete restart of the TuneD daemon is needed due to a debugging request switched on or off.
			klog.V(4).Infof("debug control triggering tuned restart")
			restart = true
			if change.debug {
				c.daemon.restart |= ctrlDebug
			} else {
				c.daemon.restart &= ^ctrlDebug
			}
		}

		// Does the current TuneD process have the reapply_sysctl option turned on?
		reapplySysctl := c.tunedMainCfg.Section("").Key("reapply_sysctl").MustBool()
		if reapplySysctl != change.reapplySysctl {
			klog.V(4).Infof("reapplySysctl rewriting configuration file")

			if err = iniCfgSetKey(c.tunedMainCfg, "reapply_sysctl", !reapplySysctl); err != nil {
				return false, err
			}
			err = iniFileSave(tunedMainConfPath, c.tunedMainCfg)
			if err != nil {
				return false, fmt.Errorf("failed to write global TuneD configuration file: %v", err)
			}
			klog.V(4).Infof("reapplySysctl triggering tuned restart")
			restart = true // A complete restart of the TuneD daemon is needed due to configuration change in tuned-main.conf file.
		}
	}

	// failures pertaining to deferred updates are not critical
	_ = c.handleDaemonReloadRestartRequest(change, reload, restart)

	cfgUpdated, err = c.changeSyncerRestartOrReloadTuneD()
	klog.V(2).Infof("changeSyncerTuneD() configuration updated: %v", cfgUpdated)
	if err != nil {
		return false, err
	}

	return err == nil, err
}

func (c *Controller) handleDaemonReloadRestartRequest(change Change, reload, restart bool) error {
	if !reload && !restart {
		// nothing to do
		return nil
	}

	if !change.deferred || change.nodeRestart {
		if reload {
			klog.V(2).Infof("immediate update, setting reload flag")
			c.daemon.restart |= ctrlReload
		}
		if restart {
			klog.V(2).Infof("immediate update, setting restart flag")
			c.daemon.restart |= ctrlRestart
		}
		return nil
	}

	klog.V(2).Infof("deferred update profile unpacked %q profile effective %q", c.daemon.profileFingerprintUnpacked, c.daemon.profileFingerprintEffective)

	if c.daemon.profileFingerprintUnpacked == c.daemon.profileFingerprintEffective {
		klog.V(2).Infof("deferred update, but it seems already applied (profile unpacked == profile effective), nothing to do")
		return nil
	}

	err := c.storeDeferredUpdate(c.daemon.profileFingerprintUnpacked)
	// on restart, we will have the deferred flag but the profileFingerprint will match. So the change must take effect immediately
	klog.Infof("deferred update: TuneD daemon won't be reloaded until next restart or immediate update (err=%v)", err)
	if err != nil {
		return err
	}

	// trigger status update
	c.wqTuneD.Add(wqKeyTuned{kind: wqKindDaemon, change: Change{
		profileStatus: true,
		message:       fmt.Sprintf("status change for deferred update %q", change.recommendedProfile),
	}})
	return nil
}

func (c *Controller) changeSyncerRestartOrReloadTuneD() (bool, error) {
	klog.V(2).Infof("changeSyncerRestartOrReloadTuneD()")
	if (c.daemon.restart & ctrlRestart) != 0 {
		// Complete restart of the TuneD daemon needed.  For example, debuging option is used or an option in tuned-main.conf file changed).
		return true, c.tunedRestart()
	}
	if (c.daemon.restart & ctrlReload) != 0 {
		return true, c.tunedReload()
	}
	return false, nil
}

// Method changeSyncer performs k8s Profile object updates and TuneD daemon
// reloads as needed.  Returns indication whether the change was successfully
// synced and an error.  Only critical errors are returned, as non-nil errors
// will cause restart of the main control loop -- the changeWatcher() method.
func (c *Controller) changeSyncer(change Change) (bool, error) {
	klog.V(2).Infof("changeSyncer(%s)", change.String())
	defer klog.V(2).Infof("changeSyncer(%s) done", change.String())

	// Sync internal status after a node restart
	c.changeSyncerPostReloadOrRestart(change)

	// Sync k8s Profile status if/when needed.
	if !c.changeSyncerProfileStatus(change) {
		return false, nil
	}

	// Extract TuneD configuration from k8s objects and restart/reload TuneD as needed.
	return c.changeSyncerTuneD(change)
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

			c.changeCh <- workqueueKey.change
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

func (c *Controller) daemonMessage(change Change, message string) string {
	if len(message) > 0 {
		return message
	}
	if len(change.message) > 0 {
		return change.message
	}
	return c.daemon.stderr
}

// Method updateTunedProfile updates a Tuned Profile with information to report back
// to the operator.  Note this method must be called only when the TuneD daemon is
// not reloading.
func (c *Controller) updateTunedProfile(change Change) (err error) {
	var bootcmdline string

	if bootcmdline, err = GetBootcmdline(); err != nil {
		// This should never happen unless something is seriously wrong (e.g. TuneD
		// daemon no longer uses tunedBootcmdlineFile).  Do not continue.
		return fmt.Errorf("unable to get kernel command-line parameters: %v", err)
	}

	node, err := c.getNodeForProfile(c.nodeName)
	if err != nil {
		return err
	}

	if node.ObjectMeta.Annotations == nil {
		node.ObjectMeta.Annotations = map[string]string{}
	}

	bootcmdlineAnnotVal, bootcmdlineAnnotSet := node.ObjectMeta.Annotations[tunedv1.TunedBootcmdlineAnnotationKey]
	if !bootcmdlineAnnotSet || bootcmdlineAnnotVal != bootcmdline {
		annotations := map[string]string{tunedv1.TunedBootcmdlineAnnotationKey: bootcmdline}
		err = c.updateNodeAnnotations(node, annotations)
		if err != nil {
			return err
		}
	}

	return c.updateTunedProfileStatus(context.TODO(), change)
}

func (c *Controller) updateTunedProfileStatus(ctx context.Context, change Change) error {
	activeProfile, err := getActiveProfile()
	if err != nil {
		return err
	}

	profile, err := c.listers.TunedProfiles.Get(c.nodeName)
	if err != nil {
		return fmt.Errorf("failed to get Profile %s: %v", c.nodeName, err)
	}

	var message string
	wantsDeferred := util.HasDeferredUpdateAnnotation(profile.Annotations)
	isApplied := (c.daemon.profileFingerprintUnpacked == c.daemon.profileFingerprintEffective)
	daemonStatus := c.daemon.status

	klog.V(4).Infof("daemonStatus(): change: deferred=%v applied=%v nodeRestart=%v", wantsDeferred, isApplied, change.nodeRestart)
	if (wantsDeferred && !isApplied) && !change.nodeRestart { // avoid setting the flag on updates deferred -> immediate
		daemonStatus |= scDeferred
		recommendProfile, err := TunedRecommendFileRead()
		if err == nil {
			klog.V(2).Infof("updateTunedProfileStatus(): recommended profile %q (deferred)", recommendProfile)
			message = c.daemonMessage(change, recommendProfile)
		} else {
			// just log and carry on, we will use this info to clarify status conditions
			klog.Errorf("%s", err.Error())
		}
	}

	statusConditions := computeStatusConditions(daemonStatus, message, profile.Status.Conditions)
	klog.V(4).Infof("computed status conditions: %#v", statusConditions)
	c.daemon.status = daemonStatus

	if profile.Status.TunedProfile == activeProfile &&
		conditionsEqual(profile.Status.Conditions, statusConditions) {
		klog.V(2).Infof("updateTunedProfileStatus(): no need to update status of Profile %s", profile.Name)
		return nil
	}

	profile = profile.DeepCopy() // never update the objects from cache

	profile.Status.TunedProfile = activeProfile
	profile.Status.Conditions = statusConditions
	_, err = c.clients.Tuned.TunedV1().Profiles(operandNamespace).UpdateStatus(ctx, profile, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Profile %s status: %v", profile.Name, err)
	}
	return nil
}

// storeDeferredUpdate sets the node state (on storage, like disk) to signal
// there is a deferred update pending.
func (c *Controller) storeDeferredUpdate(deferredFP string) (derr error) {
	// "overwriting" is fine, because we only want an empty data file.
	// all the races are benign, so we go for the simplest approach
	fp, err := os.Create(tunedDeferredUpdateEphemeralFilePath)
	if err != nil {
		return err
	}
	_ = fp.Close() // unlikely to fail, we don't write anything

	// overwriting files is racy, and output can be mixed in.
	// the safest approach is to create a temporary file, write
	// the full content to it and then rename it, because rename(2)
	// is atomic, this is guaranteed safe and race-free.
	dst, err := os.CreateTemp(ocpTunedHome, "ocpdeferred")
	if err != nil {
		return err
	}
	tmpName := dst.Name()
	defer func() {
		if dst == nil {
			return
		}
		derr = dst.Close()
		os.Remove(dst.Name()) // avoid littering with tmp files
	}()
	if _, err := dst.WriteString(deferredFP); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	dst = nil // avoid double close()s, the second will fail
	return os.Rename(tmpName, tunedDeferredUpdatePersistentFilePath)
}

// recoverAndClearDeferredUpdate detects the presence and removes the persistent
// deferred updates file.
// Returns:
//   - The defered profile fingerprint.
//   - A boolean indicating the absence of the ephemeral deferred update file.
//     If the file is absent, a node restart is assumed and true is returned.
//   - Error if any.
func (c *Controller) recoverAndClearDeferredUpdate() (string, bool, error) {
	isReboot := false

	deferredFP, err := os.ReadFile(tunedDeferredUpdatePersistentFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			klog.Infof("recover: no pending deferred change")
			return "", isReboot, nil
		}
		klog.Infof("recover: failed to restore pending deferred change: %v", err)
		return "", isReboot, err
	}
	pendingFP := strings.TrimSpace(string(deferredFP))
	err = os.Remove(tunedDeferredUpdatePersistentFilePath)
	klog.Infof("recover: found pending deferred update: %q", pendingFP)

	if _, errEph := os.Stat(tunedDeferredUpdateEphemeralFilePath); errEph != nil {
		if os.IsNotExist(errEph) {
			isReboot = true
		} else {
			klog.Infof("recover: failed to detect node restart, assuming not: %v", err)
			return "", false, errEph
		}
	}
	return pendingFP, isReboot, err
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
			if workqueueKey.kind == wqKindProfile && workqueueKey.name == c.nodeName {
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
	c.tunedMainCfg, err = iniFileLoad(tunedMainConfPath)
	if err != nil {
		return fmt.Errorf("failed to load global TuneD configuration file: %v", err)
	}

	// Use less aggressive per-item only exponential rate limiting for both wqKube and wqTuneD.
	// Start retrying at 100ms with a maximum of 1800s.
	c.wqKube = workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(100*time.Millisecond, 1800*time.Second))
	c.wqTuneD = workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(100*time.Millisecond, 1800*time.Second))

	tunedInformerFactory := tunedinformers.NewSharedInformerFactoryWithOptions(
		c.clients.Tuned,
		ntoconfig.ResyncPeriod(),
		tunedinformers.WithNamespace(operandNamespace))

	tpInformer := tunedInformerFactory.Tuned().V1().Profiles()
	c.listers.TunedProfiles = tpInformer.Lister().Profiles(operandNamespace)
	if _, err = tpInformer.Informer().AddEventHandler(c.informerEventHandler(wqKeyKube{kind: wqKindProfile})); err != nil {
		return err
	}

	tunedInformerFactory.Start(c.stopCh) // Tuned/Profile

	// Wait for the caches to be synced before starting worker(s).
	klog.V(1).Info("waiting for informer caches to sync")
	ok := cache.WaitForCacheSync(c.stopCh,
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

	if c.pendingChange != nil {
		klog.Infof("processing pending change: %s", c.pendingChange.String())
		c.wqTuneD.Add(wqKeyTuned{kind: wqKindDaemon, change: *c.pendingChange})
		c.pendingChange = nil
	}

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
			return fmt.Errorf("failed to start monitoring filesystem events on %q: %v", element, err)
		}
		klog.Infof("monitoring filesystem events on %q", element)
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

		case ch := <-c.changeCh:
			klog.V(2).Infof("changeCh")

			synced, err := c.changeSyncer(ch)
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

func RunInCluster(stopCh <-chan struct{}, version string) error {
	klog.Infof("starting in-cluster %s %s", programName, version)

	if err := switchTunedHome(); err != nil {
		return err
	}

	if err := prepareOpenShiftTunedDir(); err != nil {
		return err
	}

	// Running in a k8s cluster pod with ocpTunedSystemdService unit present.
	if err := writeOpenShiftTunedImageEnv(); err != nil {
		return err
	}

	// The original way of running TuneD inside of a k8s container.
	c, err := newController(getNodeName(), stopCh)
	if err != nil {
		panic(err.Error())
	}

	if err := os.MkdirAll(ocpTunedRunDir, os.ModePerm); err != nil {
		panic(err.Error())
	}

	profiles, recommended, err := profilesRepackPath(tunedRecommendFile, tunedProfilesDirCustom)
	if err != nil {
		// keep going, immediate updates are expected to work as usual
		// and we expect the vast majority of updates to be immediate anyway
		klog.Errorf("error repacking the profile: %v", err)
		klog.Infof("deferred updates likely broken")
	}

	profileFP := profilesFingerprint(profiles, recommended)
	c.daemon.profileFingerprintUnpacked = profileFP
	klog.Infof("starting: profile fingerprint unpacked %q", profileFP)

	deferredFP, isNodeReboot, err := c.recoverAndClearDeferredUpdate()
	if err != nil {
		klog.ErrorS(err, "unable to recover the pending update")
	} else if !isNodeReboot {
		klog.Infof("starting: does not seem a node reboot, but a daemon restart. Ignoring pending deferred updates (if any)")
	} else if deferredFP == "" {
		klog.Infof("starting: node reboot, but no pending deferred update")
	} else {
		klog.Infof("starting: recovered and cleared pending deferred update %q (fingerprint=%q)", recommended, deferredFP)
		c.pendingChange = &Change{
			profile:            true,
			nodeRestart:        true,
			recommendedProfile: recommended,
			message:            deferredFP,
		}
	}

	return retryLoop(c)
}

func RunOutOfClusterOneShot(stopCh <-chan struct{}, version string) error {
	klog.Infof("starting out-of-cluster %s %s", programName, version)

	if err := prepareOpenShiftTunedDir(); err != nil {
		return err
	}

	// Do not block the kubelet by running TuneD for longer than 60s.
	err := TunedRunNoDaemon(60 * time.Second)
	if err != nil {
		// Just log this error.  We do not want to block the kubelet by failing the systemd service.
		klog.Errorf("failed to run TuneD in one-shot mode: %v", err)
	}

	return nil
}
