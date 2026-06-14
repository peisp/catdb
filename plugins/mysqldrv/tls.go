package mysqldrv

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"github.com/go-sql-driver/mysql"

	"catdb/internal/dbdriver"
)

// buildTLSConfig translates dbdriver.SSLConfig to a *tls.Config.
//
// Mode follows libpq conventions:
//
//	disable      → no TLS (caller skips registration entirely)
//	prefer       → use built-in "preferred"   (TLS if offered, plaintext otherwise)
//	require      → use built-in "skip-verify" (TLS, but trust any cert)
//	verify-ca    → tls.Config with our CAs, ServerName empty (only chain checked)
//	verify-full  → tls.Config with CAs + ServerName (hostname check on)
//
// For "prefer" and "require" the caller should use the well-known TLS-config
// names from the mysql driver directly; this builder returns nil + a sentinel
// name in those cases so the DSN can reference them.
func buildTLSConfig(ssl *dbdriver.SSLConfig) (cfg *tls.Config, sentinelName string, err error) {
	if ssl == nil {
		return nil, "", nil
	}
	switch ssl.Mode {
	case "", "disable":
		return nil, "", nil
	case "prefer":
		return nil, "preferred", nil
	case "require":
		// "skip-verify" is the only built-in flavour go-sql-driver exposes for
		// "encrypted but don't verify".
		return nil, "skip-verify", nil
	case "verify-ca", "verify-full":
		// fall through
	default:
		return nil, "", fmt.Errorf("mysqldrv: unknown ssl.mode %q", ssl.Mode)
	}

	tcfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if ssl.CACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(ssl.CACert)) {
			return nil, "", fmt.Errorf("mysqldrv: ssl.caCert is not a valid PEM bundle")
		}
		tcfg.RootCAs = pool
	}
	if ssl.ClientCert != "" || ssl.ClientKey != "" {
		if ssl.ClientCert == "" || ssl.ClientKey == "" {
			return nil, "", fmt.Errorf("mysqldrv: ssl.clientCert and ssl.clientKey must both be set")
		}
		cert, err := tls.X509KeyPair([]byte(ssl.ClientCert), []byte(ssl.ClientKey))
		if err != nil {
			return nil, "", fmt.Errorf("mysqldrv: load client keypair: %w", err)
		}
		tcfg.Certificates = []tls.Certificate{cert}
	}

	switch ssl.Mode {
	case "verify-ca":
		// Chain must validate but the hostname is not checked.
		tcfg.InsecureSkipVerify = false
		tcfg.ServerName = "" // ServerName is irrelevant when full verification is off via the callback below.
		tcfg.VerifyPeerCertificate = verifyChainOnly(tcfg.RootCAs)
		// We DO want crypto/tls to skip its built-in name verification while
		// still requiring a valid chain via our callback. The combination is:
		// InsecureSkipVerify=true + VerifyPeerCertificate enforcing the chain.
		tcfg.InsecureSkipVerify = true
	case "verify-full":
		if ssl.ServerName != "" {
			tcfg.ServerName = ssl.ServerName
		}
	}
	return tcfg, "", nil
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

// registerTLS picks a name and registers the *tls.Config under it. Returns
// the name to embed in the DSN's tls= parameter. Caller may use the returned
// deregister to free the global entry on Connection.Close.
func registerTLS(cfg *tls.Config) (string, func(), error) {
	name := "catdb-tls-" + randomID()
	if err := mysql.RegisterTLSConfig(name, cfg); err != nil {
		return "", nil, fmt.Errorf("mysqldrv: RegisterTLSConfig: %w", err)
	}
	dereg := func() { mysql.DeregisterTLSConfig(name) }
	return name, dereg, nil
}
