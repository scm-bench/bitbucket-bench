package bitbucketdc

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// tlsInsecureConfig is isolated in its own file so the one place that disables
// certificate verification is easy to find and audit. It is only reachable via
// the explicit scan.insecure setting.
func tlsInsecureConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in via scan.insecure
}

// tlsWithCAFile trusts the certificate authorities in a PEM bundle on top of
// the system pool. Read and parsed here, at startup: a bundle that is missing
// or holds no certificate is a configuration error to report before the first
// request, not a handshake failure to puzzle over after it.
func tlsWithCAFile(path string) (*tls.Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("scan.caFile: %w", err)
	}
	pool, err := systemCertPool()
	if err != nil || pool == nil {
		// Windows before Go 1.18 and some minimal containers have no
		// enumerable system pool; the bundle alone is then what is trusted.
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("scan.caFile %s: no PEM certificate found in it", path)
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}

// systemCertPool is a variable so a test can stand in a platform with no
// enumerable system pool.
var systemCertPool = x509.SystemCertPool
