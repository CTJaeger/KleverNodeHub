package agent

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/crypto"
)

// newDashboardLikeServer serves TLS the way the dashboard does (leaf only) and
// offers the CA at /api/agent/ca.
func newDashboardLikeServer(t *testing.T, ca *crypto.CA, offerCA bool) *httptest.Server {
	t.Helper()
	srvTLS, err := crypto.DashboardTLSConfig(ca)
	if err != nil {
		t.Fatal(err)
	}
	srvTLS.ClientAuth = tls.RequestClientCert // as the dashboard serves browsers
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/ca" {
			if !offerCA {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(crypto.EncodeCertPEM(ca.Certificate.Raw))
		}
	}))
	srv.TLS = srvTLS
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestRegistrationTLSConfig_PinsDashboardCA(t *testing.T) {
	ca, err := crypto.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	srv := newDashboardLikeServer(t, ca, true)

	other, _ := crypto.NewCA()
	get := func(fp string) error {
		var pinned *x509.Certificate
		if fp != "" {
			var err error
			if pinned, err = fetchDashboardCA(srv.URL, fp); err != nil {
				return err
			}
		}
		cfg, err := registrationTLSConfig(srv.URL, fp, pinned)
		if err != nil {
			return err
		}
		resp, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}).Get(srv.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}

	if err := get(crypto.Fingerprint(ca.Certificate)); err != nil {
		t.Errorf("pinned dashboard CA rejected: %v", err)
	}
	if err := get(crypto.Fingerprint(other.Certificate)); err == nil {
		t.Error("dashboard accepted although its CA does not match the pin")
	}
	if err := get(""); err == nil {
		t.Error("self-signed dashboard accepted without a pin")
	}
}

// A dashboard that doesn't offer /api/agent/ca and sends no CA in its chain
// can't be verified: the token must not be sent.
func TestRegistrationTLSConfig_NoCAAvailable(t *testing.T) {
	ca, err := crypto.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	srv := newDashboardLikeServer(t, ca, false)
	fp := crypto.Fingerprint(ca.Certificate)

	pinned, err := fetchDashboardCA(srv.URL, fp)
	if err != nil || pinned != nil {
		t.Fatalf("missing endpoint must yield no CA and no error, got %v / %v", pinned, err)
	}
	cfg, err := registrationTLSConfig(srv.URL, fp, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}).Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Error("dashboard accepted although its certificate could not be verified")
	}
}
