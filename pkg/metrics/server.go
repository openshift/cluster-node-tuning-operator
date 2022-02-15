package metrics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/fsnotify.v1"

	"k8s.io/klog/v2"
)

const (
	tlsSecretDir = "/etc/secrets"
	tlsCert      = tlsSecretDir + "/tls.crt"
	tlsKey       = tlsSecretDir + "/tls.key"
)

type Server struct {
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

	bindAddr := fmt.Sprintf(":%d", port)
	router := http.NewServeMux()
	router.Handle("/metrics", handler)
	srv := &http.Server{
		Addr:    bindAddr,
		Handler: router,
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

// RunServer starts the server, and watches the tlsCert and tlsKey for certificate changes.
func RunServer(port int, ctx context.Context) error {
	srv := buildServer(port)
	if srv == nil {
		return fmt.Errorf("failed to build server with port %d", port)
	}

	go startServer(srv)

	// Set up and start the file watcher.
	watcher, err := fsnotify.NewWatcher()
	if watcher == nil || err != nil {
		return fmt.Errorf("failed to create file watcher, cert/key rotation will be disabled  %v", err)
	} else {
		defer watcher.Close()
		if err := watcher.Add(tlsSecretDir); err != nil {
			return fmt.Errorf("failed to add %v to watcher, cert/key rotation will be disabled: %v", tlsSecretDir, err)
		}
	}

	origCertChecksum := checksumFile(tlsCert)
	origKeyChecksum := checksumFile(tlsKey)

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

			if certsChanged(origCertChecksum, origKeyChecksum) {
				// Update file checksums with latest files.
				origCertChecksum = checksumFile(tlsCert)
				origKeyChecksum = checksumFile(tlsKey)

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

// Determine if the certificates have changed and need to be updated.
// Returns true if both files have changed AND neither is an empty file.
func certsChanged(origCertChecksum []byte, origKeyChecksum []byte) bool {
	// Check if both files exist.
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

	klog.V(2).Infof("certificate checksums before: %x, %x. checksums after: %x, %x", origCertChecksum, origKeyChecksum, currentCertChecksum, currentKeyChecksum)
	// Check if the non-empty certificate/key files have actually changed.
	if !bytes.Equal(origCertChecksum, currentCertChecksum) && !bytes.Equal(origKeyChecksum, currentKeyChecksum) {
		klog.Infof("cert and key changed, need to restart the server.")
		return true
	}

	return false
}

// Compute the sha256 checksum for file 'fName'.
func checksumFile(fName string) []byte {
	file, err := os.Open(fName)
	if err != nil {
		klog.Infof("failed to open file %v for checksum: %v", fName, err)
	}
	defer file.Close()

	hash := sha256.New()

	if _, err = io.Copy(hash, file); err != nil {
		klog.Infof("failed to compute checksum for file %v: %v", fName, err)
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
