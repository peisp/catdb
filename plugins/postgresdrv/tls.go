package postgresdrv

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"catdb/internal/dbdriver"
)

// buildTLSConfig translates dbdriver.SSLConfig to a *tls.Config, following
// the same libpq mode semantics as mysqldrv:
//
//	disable      → nil (no TLS)
//	prefer       → TLS without verification; caller adds a plaintext fallback
//	require      → TLS without verification
//	verify-ca    → chain must validate against our CAs, hostname not checked
//	verify-full  → chain + hostname verification (ServerName defaults to host)
func buildTLSConfig(ssl *dbdriver.SSLConfig, host string) (*tls.Config, error) {
	if ssl == nil {
		return nil, nil
	}
	switch ssl.Mode {
	case "", "disable":
		return nil, nil
	case "prefer", "require", "verify-ca", "verify-full":
		// fall through
	default:
		return nil, fmt.Errorf("postgresdrv: unknown ssl.mode %q", ssl.Mode)
	}

	tcfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if ssl.CACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(ssl.CACert)) {
			return nil, fmt.Errorf("postgresdrv: ssl.caCert is not a valid PEM bundle")
		}
		tcfg.RootCAs = pool
	}
	if ssl.ClientCert != "" || ssl.ClientKey != "" {
		if ssl.ClientCert == "" || ssl.ClientKey == "" {
			return nil, fmt.Errorf("postgresdrv: ssl.clientCert and ssl.clientKey must both be set")
		}
		cert, err := tls.X509KeyPair([]byte(ssl.ClientCert), []byte(ssl.ClientKey))
		if err != nil {
			return nil, fmt.Errorf("postgresdrv: load client keypair: %w", err)
		}
		tcfg.Certificates = []tls.Certificate{cert}
	}

	switch ssl.Mode {
	case "prefer", "require":
		tcfg.InsecureSkipVerify = true
	case "verify-ca":
		// Skip crypto/tls's built-in name check but still require a valid
		// chain via the callback.
		tcfg.InsecureSkipVerify = true
		tcfg.VerifyPeerCertificate = verifyChainOnly(tcfg.RootCAs)
	case "verify-full":
		tcfg.ServerName = ssl.ServerName
		if tcfg.ServerName == "" {
			tcfg.ServerName = host
		}
	}
	return tcfg, nil
}

func verifyChainOnly(roots *x509.CertPool) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("ssl: server presented no certificate")
		}
		certs := make([]*x509.Certificate, 0, len(rawCerts))
		for i, raw := range rawCerts {
			c, err := x509.ParseCertificate(raw)
			if err != nil {
				return fmt.Errorf("ssl: parse cert %d: %w", i, err)
			}
			certs = append(certs, c)
		}
		opts := x509.VerifyOptions{
			Roots:         roots,
			Intermediates: x509.NewCertPool(),
		}
		for _, c := range certs[1:] {
			opts.Intermediates.AddCert(c)
		}
		_, err := certs[0].Verify(opts)
		return err
	}
}
