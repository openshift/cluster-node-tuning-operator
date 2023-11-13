package metrics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"k8s.io/klog/v2"

	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/fsnotify.v1"
)

const (
	tlsSecretDir = "/etc/secrets"
	tlsCert      = tlsSecretDir + "/tls.crt"
	tlsKey       = tlsSecretDir + "/tls.key"

	authCADir                = "/tmp/metrics-client-ca"
	authCAFile               = authCADir + "/ca.crt"
	AuthConfigMapNamespace   = "kube-system"
	AuthConfigMapName        = "extension-apiserver-authentication"
	AuthConfigMapClientCAKey = "client-ca-file"
)

type Server struct {
}

// DumpCA writes the root certificate bundle which is used to verify client certificates
// on incoming requests to 'authCAFile' file.
func DumpCA(ca string) error {
	if err := util.Mkdir(authCADir); err != nil {
		return fmt.Errorf("failed to create directory %q: %v", authCADir, err)
	}

	f, err := os.Create(authCAFile)
	if err != nil {
		return fmt.Errorf("failed to create file %q: %v", authCAFile, err)
	}
	defer f.Close()
	if _, err = f.WriteString(ca); err != nil {
		return fmt.Errorf("failed to write file %q: %v", authCAFile, err)
	}

	return nil
}

func buildServer(port int) *http.Server {
	if port <= 0 {
		klog.Error("invalid port for metric server")
		return nil
	}

	handler := promhttp.HandlerFor(
		registry,
		promhttp.HandlerOpts{
			ErrorHandling: promhttp.HTTPErrorOnError,
		},
	)

	tlsConfig := &tls.Config{}
	caCert, err := ioutil.ReadFile(authCAFile)
	if err == nil {
		caCertPool := x509.NewCertPool()
		if caCertPool.AppendCertsFromPEM(caCert) {
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
			tlsConfig.ClientCAs = caCertPool
			// Default minimum version is TLS 1.2, previous versions are insecure and deprecated.
			tlsConfig.MinVersion = tls.VersionTLS12
			tlsConfig.CipherSuites = []uint16{
				// Drop
				// - 64-bit block cipher 3DES as it is vulnerable to SWEET32 attack.
				// - CBC encryption method.
				// - RSA key exchange.
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			}
			tlsConfig.NextProtos = []string{"http/1.1"} // CVE-2023-44487
		} else {
			klog.Error("failed to parse %q", authCAFile)
		}
	} else {
		klog.Errorf("failed to read %q: %v", authCAFile, err)
	}
	if tlsConfig.ClientCAs == nil {
		klog.Infof("continuing without client authentication")
	}

	bindAddr := fmt.Sprintf(":%d", port)
	router := http.NewServeMux()
	router.Handle("/metrics", handler)
	srv := &http.Server{
		Addr:      bindAddr,
		Handler:   router,
		TLSConfig: tlsConfig,
	}

	return srv
}

func startServer(srv *http.Server) {
	klog.Infof("starting metrics server")
	if err := srv.ListenAndServeTLS(tlsCert, tlsKey); err != nil && err != http.ErrServerClosed {
		klog.Errorf("error from metrics server: %v", err)
	}
}

func stopServer(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	klog.Infof("stopping metrics server")
	if err := srv.Shutdown(ctx); err != nil {
		klog.Errorf("error or timeout stopping metrics listener: %v", err)
	}
}

func (Server) Start(ctx context.Context) error {
	return RunServer(MetricsPort, ctx)
}

// RunServer starts the server, and watches the tlsCert, tlsKey and authCAFile for changes.
// If a change happens to both tlsCert and tlsKey or authCAFile, the metrics server is rebuilt
// and restarted with the current files.  Every non-nil return from this function is fatal
// and will restart the whole operator.
func RunServer(port int, ctx context.Context) error {
	// Set up and start the file watcher.
	watcher, err := fsnotify.NewWatcher()
	if watcher == nil || err != nil {
		klog.Errorf("failed to create file watcher, cert/key rotation will be disabled %v", err)
	} else {
		defer watcher.Close()

		if err := util.Mkdir(authCADir); err != nil {
			return fmt.Errorf("failed to create directory %q: %v", authCADir, err)
		}

		if err = watcher.Add(authCADir); err != nil {
			klog.Errorf("failed to add %v to watcher, CA client authentication and rotation will be disabled: %v", authCADir, err)
		} else {
			if ok, _ := fileExistsAndNotEmpty(authCAFile); !ok {
				// authCAFile does not exist (or is empty); wait for it to be created.
				select {
				case <-ctx.Done():
					return nil
				case event := <-watcher.Events:
					klog.V(2).Infof("event from filewatcher on file: %v, event: %v", event.Name, event.Op)

					if event.Name == authCAFile {
						if ok, _ := fileExistsAndNotEmpty(authCAFile); ok {
							// authCAFile is now created and is not empty.
							break
						}
					}

				case err = <-watcher.Errors:
					klog.Warningf("error from metrics server CA client authentication file watcher: %v", err)
				}
			}
		}

		if err = watcher.Add(tlsSecretDir); err != nil {
			klog.Errorf("failed to add %v to watcher, cert/key rotation will be disabled: %v", tlsSecretDir, err)
		}
	}

	srv := buildServer(port)
	if srv == nil {
		return fmt.Errorf("failed to build server with port %d", port)
	}

	go startServer(srv)

	origCertChecksum := checksumFile(tlsCert)
	origKeyChecksum := checksumFile(tlsKey)
	origAuthCAChecksum := checksumFile(authCAFile)

	for {
		select {
		case <-ctx.Done():
			stopServer(srv)
			return nil
		case event := <-watcher.Events:
			klog.V(2).Infof("event from filewatcher on file: %v, event: %v", event.Name, event.Op)

			if event.Op == fsnotify.Chmod || event.Op == fsnotify.Remove {
				continue
			}

			if certsChanged(origCertChecksum, origKeyChecksum, origAuthCAChecksum) {
				// Update file checksums with latest files.
				origCertChecksum = checksumFile(tlsCert)
				origKeyChecksum = checksumFile(tlsKey)
				origAuthCAChecksum = checksumFile(authCAFile)

				// restart server
				klog.Infof("restarting metrics server to rotate certificates")
				stopServer(srv)
				srv = buildServer(port)
				go startServer(srv)
			}
		case err = <-watcher.Errors:
			klog.Warningf("error from metrics server certificate file watcher: %v", err)
		}
	}
}

// Determine if both the server certificate/key or auth CA have changed and need to be updated.
// Given the server certificate/key and auth CA exist and are non-empty, returns true if
// both server certificate and key have changed or if auth CA have changed.
func certsChanged(origCertChecksum []byte, origKeyChecksum []byte, origAuthCAChecksum []byte) bool {
	// Check if all files exist.
	certNotEmpty, err := fileExistsAndNotEmpty(tlsCert)
	if err != nil {
		klog.Warningf("error checking if changed TLS cert file empty/exists: %v", err)
		return false
	}
	keyNotEmpty, err := fileExistsAndNotEmpty(tlsKey)
	if err != nil {
		klog.Warningf("error checking if changed TLS key file empty/exists: %v", err)
		return false
	}
	caNotEmpty, err := fileExistsAndNotEmpty(authCAFile)
	if err != nil {
		klog.Warningf("error checking if changed auth CA file empty/exists: %v", err)
		return false
	}

	if !certNotEmpty || !keyNotEmpty || !caNotEmpty {
		// One of the files is missing despite some file event.
		klog.V(1).Infof("certificate, key or auth CA is missing or empty, certificates will not be rotated")
		return false
	}
	currentCertChecksum := checksumFile(tlsCert)
	currentKeyChecksum := checksumFile(tlsKey)
	currentAuthCAChecksum := checksumFile(authCAFile)

	klog.V(2).Infof("certificate checksums before: %x, %x, %x. checksums after: %x, %x, %x",
		origCertChecksum, origKeyChecksum, origAuthCAChecksum, currentCertChecksum, currentKeyChecksum, currentAuthCAChecksum)
	// Check if the non-empty certificate/key or auth CA files have actually changed.
	if !bytes.Equal(origCertChecksum, currentCertChecksum) && !bytes.Equal(origKeyChecksum, currentKeyChecksum) ||
		!bytes.Equal(origAuthCAChecksum, currentAuthCAChecksum) {
		klog.Infof("cert and key or auth CA changed, need to restart the metrics server")
		return true
	}

	return false
}

// Compute the sha256 checksum for file 'fName'.
func checksumFile(fName string) []byte {
	file, err := os.Open(fName)
	if err != nil {
		klog.Errorf("failed to open file %v for checksum: %v", fName, err)
	}
	defer file.Close()

	hash := sha256.New()

	if _, err = io.Copy(hash, file); err != nil {
		klog.Errorf("failed to compute checksum for file %v: %v", fName, err)
	}

	return hash.Sum(nil)
}

// Check if a file exists and has file.Size() not equal to 0.
// Returns any error returned by os.Stat other than os.ErrNotExist.
func fileExistsAndNotEmpty(fName string) (bool, error) {
	if fi, err := os.Stat(fName); err == nil {
		return (fi.Size() != 0), nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		// Some other error, file may not exist.
		return false, err
	}
}
