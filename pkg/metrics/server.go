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
	"net/http"
	"os"
	"time"

	"k8s.io/klog/v2"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/fsnotify.v1"
)

const (
	tlsSecretDir = "/etc/secrets"
	tlsCert      = tlsSecretDir + "/tls.crt"
	tlsKey       = tlsSecretDir + "/tls.key"

	AuthConfigMapNamespace   = "kube-system"
	AuthConfigMapName        = "extension-apiserver-authentication"
	AuthConfigMapClientCAKey = "client-ca-file"
)

type Server struct {
	caBundle   string
	caBundleCh chan string
}

var (
	server Server
)

func init() {
	server = Server{caBundleCh: make(chan string)}
}

// DumpCA stores the root certificate bundle which is used to verify metric server
// client certificates.  It uses an unbuffered channel to store the data and it
// it does so only if the CA bundle changed from the current CA bundle on record.
func DumpCA(caBundle string) {
	if caBundle != server.caBundle {
		server.caBundleCh <- caBundle
	}
}

func buildServer(port int, caBundle string) *http.Server {
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
	caCertPool := x509.NewCertPool()
	var clientAuthHandler http.Handler = handler

	if caCertPool.AppendCertsFromPEM([]byte(caBundle)) {
		// Request client certificates; verify in handler to return "401 Unauthorized" instead of a TLS alert.
		tlsConfig.ClientAuth = tls.RequestClientCert
		tlsConfig.ClientCAs = caCertPool
		// Default minimum version is TLS 1.3.  PQ algorithms will only be supported in TLS 1.3+.
		// Hybrid key agreements for TLS 1.3 X25519MLKEM768 is supported by default in go 1.24.
		tlsConfig.MinVersion = tls.VersionTLS13
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

		// Wrap the handler to check for client certificates.
		clientAuthHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
				// No client certificate provided, return 401 Unauthorized.
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			// With RequestClientCert we now have PeerCertificate(s) that have not
			// been verified by the TLS layer.

			intermediates := x509.NewCertPool()
			for _, cert := range r.TLS.PeerCertificates[1:] {
				intermediates.AddCert(cert)
			}
			verifyOpts := x509.VerifyOptions{
				Roots:         caCertPool,
				Intermediates: intermediates,
				KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			}
			if _, err := r.TLS.PeerCertificates[0].Verify(verifyOpts); err != nil {
				klog.Errorf("Client certificate verification failed: %v", err)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Valid client certificate received, serve metrics.
			handler.ServeHTTP(w, r)
		})
	} else {
		klog.Errorf("failed to parse root certificate bundle of the metrics server, client authentication will be disabled")
	}
	if tlsConfig.ClientCAs == nil {
		klog.Infof("continuing without client authentication")
	}

	bindAddr := fmt.Sprintf(":%d", port)
	router := http.NewServeMux()
	router.Handle("/metrics", clientAuthHandler)
	srv := &http.Server{
		Addr:      bindAddr,
		Handler:   router,
		TLSConfig: tlsConfig,
	}

	return srv
}

func startServer(srv *http.Server) {
	klog.Infof("starting metrics server on %s", srv.Addr)
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

// RunServer starts the server, and watches the tlsCert and tlsKey for changes.
// If a change happens to both tlsCert and tlsKey, the metrics server is rebuilt
// and restarted with the current files.  Every non-nil return from this function is fatal
// and will restart the whole operator.
func RunServer(port int, ctx context.Context) error {
	// Set up and start the file watcher.
	watcher, err := fsnotify.NewWatcher()
	if watcher == nil || err != nil {
		klog.Errorf("failed to create file watcher, cert/key rotation will be disabled %v", err)
	} else {
		defer watcher.Close()

		if err = watcher.Add(tlsSecretDir); err != nil {
			klog.Errorf("failed to add %v to watcher, cert/key rotation will be disabled: %v", tlsSecretDir, err)
		}

		// Wait for the root certificate bundle of the metrics server for client authentication.
		// The bundle is sent from a ConfigMap via a channel by the operator.
		server.caBundle = <-server.caBundleCh
	}

	srv := buildServer(port, server.caBundle)
	if srv == nil {
		return fmt.Errorf("failed to build server with port %d", port)
	}

	go startServer(srv)

	origCertChecksum := checksumFile(tlsCert)
	origKeyChecksum := checksumFile(tlsKey)

	for {
		restartServer := false
		select {
		case <-ctx.Done():
			stopServer(srv)
			return nil
		case server.caBundle = <-server.caBundleCh:
			restartServer = true
		case event := <-watcher.Events:
			klog.V(2).Infof("event from filewatcher on file: %v, event: %v", event.Name, event.Op)

			if event.Op == fsnotify.Chmod || event.Op == fsnotify.Remove {
				continue
			}

			if certsChanged(origCertChecksum, origKeyChecksum) {
				// Update file checksums with latest files.
				origCertChecksum = checksumFile(tlsCert)
				origKeyChecksum = checksumFile(tlsKey)
				restartServer = true
			}
		case err = <-watcher.Errors:
			klog.Warningf("error from metrics server certificate file watcher: %v", err)
		}

		if restartServer {
			// Restart the metrics server.
			klog.Infof("restarting metrics server to rotate certificates")
			stopServer(srv)
			srv = buildServer(port, server.caBundle)
			go startServer(srv)
		}
	}
}

// Determine if both the server certificate/key have changed and need to be updated.
// Given the server certificate/key exist and are non-empty, returns true if
// both server certificate and key have changed.
func certsChanged(origCertChecksum, origKeyChecksum []byte) bool {
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

	if !certNotEmpty || !keyNotEmpty {
		// One of the files is missing despite some file event.
		klog.V(1).Infof("certificate or key is missing or empty, certificates will not be rotated")
		return false
	}
	currentCertChecksum := checksumFile(tlsCert)
	currentKeyChecksum := checksumFile(tlsKey)

	klog.V(2).Infof("certificate checksums before: %x, %x. checksums after: %x, %x",
		origCertChecksum, origKeyChecksum, currentCertChecksum, currentKeyChecksum)
	// Check if the non-empty certificate/key files have actually changed.
	if !bytes.Equal(origCertChecksum, currentCertChecksum) && !bytes.Equal(origKeyChecksum, currentKeyChecksum) {
		klog.Infof("cert and key changed, need to restart the metrics server")
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
