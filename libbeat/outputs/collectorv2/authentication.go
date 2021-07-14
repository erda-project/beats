package collectorv2

import (
	"net/http"

	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/secret"
	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/secret/hmac"
)

type Secret interface {
	Secure(req *http.Request)
}

type basicSecret struct {
	username, password string
}

func (b *basicSecret) Secure(req *http.Request) {
	req.SetBasicAuth(b.username, b.password)
}

type hmacSecret struct {
	signer *hmac.Signer
}

func (h *hmacSecret) Secure(req *http.Request) {
	h.signer.SignCanonicalRequest(req)
}

type noSecret struct {
}

func (n *noSecret) Secure(req *http.Request) {
	return
}

func createSecret(cfg authConfig) Secret {
	switch cfg.Type {
	case "basic":
		return &basicSecret{
			username: cfg.Property["auth_username"],
			password: cfg.Property["auth_password"],
		}
	case "hmac":
		return &hmacSecret{
			signer: hmac.New(secret.AkSkPair{
				AccessKeyID: cfg.Property["access_key_id"],
				SecretKey:   cfg.Property["secret_key"],
			}),
		}
	default:
		return &noSecret{}
	}
}
