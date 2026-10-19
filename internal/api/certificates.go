package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Server) certificateRoutes(mux *http.ServeMux) {
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/certificates":                               s.listCertificates,
		"POST /api/v1/certificates":                              s.createCertificate,
		"GET /api/v1/certificates/{id}/public-chain":             s.certificatePublicChain,
		"GET /api/v1/certificates/{id}":                          s.getCertificate,
		"DELETE /api/v1/certificates/{id}":                       s.deleteCertificate,
		"POST /api/v1/certificates/{id}/replace":                 s.replaceCertificate,
		"POST /api/v1/certificates/{id}/renew":                   s.renewCertificate,
		"PUT /api/v1/certificates/{id}/auto-renew":               s.certificateAutoRenew,
		"POST /api/v1/certificates/{id}/orders/{order}/continue": s.continueCertificateOrder,
		"DELETE /api/v1/certificates/{id}/orders/{order}":        s.cancelCertificateOrder,
		"POST /api/v1/certificates/{id}/grants":                  s.grantCertificate,
		"DELETE /api/v1/certificates/{id}/grants":                s.revokeCertificate,
		"PUT /api/v1/certificate-accounts/{id}/credentials":      s.updateCertificateDNSCredentials,
		"GET /api/v1/certificate-accounts":                       s.listCertificateAccounts,
		"POST /api/v1/certificate-accounts":                      s.createCertificateAccount,
		"DELETE /api/v1/certificate-accounts/{id}":               s.deleteCertificateAccount,
		"POST /api/v1/certificate-accounts/{id}/check":           s.checkCertificateAccount,
		"GET /api/v1/certificate-http-nodes":                     s.getCertificateHTTPNodes,
		"GET /api/v1/certificate-managed-sources":                s.getManagedCertificateSources,
		"PUT /api/v1/certificate-managed-sources/{id}":           s.setManagedCertificateSync,
		"GET /api/v1/certificate-http-grants":                    s.getCertificateHTTPGrants,
		"POST /api/v1/certificate-http-grants":                   s.putCertificateHTTPGrant,
		"DELETE /api/v1/certificate-http-grants":                 s.deleteCertificateHTTPGrant,
	}
	// Every authenticated active user owns their certificate resources. Protocol
	// deployment additionally requires the existing config/task permissions.
	for pattern, handler := range routes {
		mux.Handle(pattern, s.requireAllPermissions(nil, handler))
	}
}
func (s *Server) listCertificates(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListCertificates(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "encryption_ready": s.store.CertificateEncryptionReady()})
}
func (s *Server) getCertificate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.store.GetCertificate(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	bindings, err := s.store.CertificateBindings(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	orders, err := s.store.CertificateOrders(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	grants := []core.CertificateGrant{}
	if item.Owned {
		grants, err = s.store.CertificateGrants(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
	}
	versions, err := s.store.CertificateVersionHistory(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"certificate": item, "bindings": bindings, "orders": orders, "grants": grants, "versions": versions})
}
func (s *Server) createCertificate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		core.Certificate
		certificates.Pair
		Days int `json:"days"`
	}
	if err := decodeJSON(w, r, &input, 400<<10); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var item core.Certificate
	var err error
	switch input.Source {
	case "acme":
		item, err = s.store.CreateACMECertificate(r.Context(), core.Certificate{Name: strings.TrimSpace(input.Name), Domains: input.Domains, Method: input.Method, KeyType: input.KeyType, AccountID: input.AccountID, DNSAccountID: input.DNSAccountID, AgentID: input.AgentID, AutoRenew: input.AutoRenew})
	case "import":
		item, err = s.store.SaveCertificatePair(r.Context(), "", input.Name, "import", input.Pair)
	case "self-signed":
		var pair certificates.Pair
		pair, err = certificates.SelfSigned(input.Domains, input.KeyType, input.Days)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		item, err = s.store.SaveCertificatePair(r.Context(), "", input.Name, "self-signed", pair)
	default:
		writeError(w, 400, "请选择 ACME、导入或自签来源")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "certificate.created", item.ID, item.Source)
	writeJSON(w, 201, item)
}
func (s *Server) replaceCertificate(w http.ResponseWriter, r *http.Request) {
	var input certificates.Pair
	if err := decodeJSON(w, r, &input, 400<<10); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	item, err := s.store.SaveCertificatePair(r.Context(), r.PathValue("id"), "", "import", input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "certificate.replaced", item.ID, "")
	writeJSON(w, 200, item)
}
func (s *Server) deleteCertificate(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteCertificate(r.Context(), r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "certificate.deleted", r.PathValue("id"), "")
	w.WriteHeader(204)
}
func (s *Server) renewCertificate(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RenewCertificate(r.Context(), r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "certificate.renewal_queued", r.PathValue("id"), "")
	writeJSON(w, 202, map[string]string{"status": "queued"})
}
func (s *Server) certificateAutoRenew(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(w, r, &input, 1024); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.store.SetCertificateAutoRenew(r.Context(), r.PathValue("id"), input.Enabled); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) continueCertificateOrder(w http.ResponseWriter, r *http.Request) {
	s.certificateOrderAction(w, r, false)
}
func (s *Server) cancelCertificateOrder(w http.ResponseWriter, r *http.Request) {
	s.certificateOrderAction(w, r, true)
}
func (s *Server) certificateOrderAction(w http.ResponseWriter, r *http.Request, cancel bool) {
	if err := s.store.ContinueCertificateOrder(r.Context(), r.PathValue("id"), r.PathValue("order"), cancel); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 202, map[string]string{"status": "queued"})
}
func (s *Server) grantCertificate(w http.ResponseWriter, r *http.Request) {
	s.certificateGrantAction(w, r, false)
}
func (s *Server) revokeCertificate(w http.ResponseWriter, r *http.Request) {
	s.certificateGrantAction(w, r, true)
}
func (s *Server) certificateGrantAction(w http.ResponseWriter, r *http.Request, remove bool) {
	var input struct {
		Username string `json:"username"`
	}
	if err := decodeJSON(w, r, &input, 1024); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.store.SetCertificateGrant(r.Context(), r.PathValue("id"), input.Username, remove); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "certificate.grant_changed", r.PathValue("id"), "")
	w.WriteHeader(204)
}
func (s *Server) listCertificateAccounts(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListCertificateAccounts(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, items)
}
func (s *Server) createCertificateAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		core.CertificateAccount
		Credentials certificates.DNSCredentials `json:"credentials"`
		EABID       string                      `json:"eab_id"`
		EABKey      string                      `json:"eab_key"`
		AcceptTerms bool                        `json:"accept_terms"`
	}
	if err := decodeJSON(w, r, &input, 32<<10); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if !s.store.CertificateEncryptionReady() {
		writeError(w, 400, "请先配置 QCH_CONFIG_ENCRYPTION_KEY")
		return
	}
	var secret any
	switch input.Kind {
	case "dns":
		client := certificates.DNSClient{Provider: input.Provider, Credentials: input.Credentials}
		if err := client.Validate(); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		input.CertificateAccount.CA = ""
		input.CertificateAccount.Email = ""
		secret = input.Credentials
	case "acme":
		if !input.AcceptTerms {
			writeError(w, 400, "请先阅读并同意签发机构服务条款")
			return
		}
		if input.Email == "" || core.ValidateACMEEmail(input.Email) != nil {
			writeError(w, 400, "请填写有效邮箱")
			return
		}
		if _, ok := certificates.Directories[input.CA]; !ok {
			writeError(w, 400, "签发机构不支持")
			return
		}
		_, key, err := certificates.NewKey("ecdsa256")
		if err != nil {
			writeInternalError(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		account, err := certificates.Register(ctx, input.CA, input.Email, certificates.ACMEAccount{Key: key, EABID: input.EABID, EABKey: input.EABKey})
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		input.CertificateAccount.Provider = ""
		secret = account
	default:
		writeError(w, 400, "账号类型无效")
		return
	}
	item, err := s.store.CreateCertificateAccount(r.Context(), input.CertificateAccount, secret)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "certificate.account_created", item.ID, item.Kind)
	writeJSON(w, 201, item)
}
func (s *Server) deleteCertificateAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteCertificateAccount(r.Context(), r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) checkCertificateAccount(w http.ResponseWriter, r *http.Request) {
	var credentials certificates.DNSCredentials
	item, err := s.store.CertificateAccountSecret(r.Context(), r.PathValue("id"), "dns", &credentials)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	err = (certificates.DNSClient{Provider: item.Provider, Credentials: credentials}).Check(ctx)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "connected"})
}
func (s *Server) getCertificateHTTPGrants(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.CertificateHTTPGrants(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, items)
}
func (s *Server) putCertificateHTTPGrant(w http.ResponseWriter, r *http.Request) {
	s.certificateHTTPGrantAction(w, r, false)
}
func (s *Server) deleteCertificateHTTPGrant(w http.ResponseWriter, r *http.Request) {
	s.certificateHTTPGrantAction(w, r, true)
}
func (s *Server) certificateHTTPGrantAction(w http.ResponseWriter, r *http.Request, remove bool) {
	var grant core.HTTPChallengeGrant
	if err := decodeJSON(w, r, &grant, 4096); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.store.SetCertificateHTTPGrant(r.Context(), grant, remove); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) certificatePublicChain(w http.ResponseWriter, r *http.Request) {
	chain, err := s.store.CertificatePublicChain(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="certificate-chain.pem"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(chain))
}

func (s *Server) updateCertificateDNSCredentials(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string                      `json:"name"`
		Credentials certificates.DNSCredentials `json:"credentials"`
	}
	if err := decodeJSON(w, r, &input, 32<<10); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	item, err := s.store.UpdateCertificateDNSCredentials(r.Context(), r.PathValue("id"), input.Name, input.Credentials)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) getCertificateHTTPNodes(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.CertificateHTTPNodes(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *Server) getManagedCertificateSources(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ManagedCertificateSources(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *Server) setManagedCertificateSync(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"`
	}
	if err := decodeJSON(w, r, &input, 1024); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.store.SetManagedCertificateSync(r.Context(), r.PathValue("id"), input.Action); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
