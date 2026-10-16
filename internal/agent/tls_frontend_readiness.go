package agent

import (
	"context"
	"fmt"
	"time"
)

const tlsIssuerBudget = 65 * time.Second

// acme_timeout is a polling limit in CertMagic, not a directory/account
// request deadline. A config reload cancels that issuer's in-flight work and
// gives the backup a separate opportunity within the deployment deadline.
func (m *tlsFrontendManager) waitReady(ctx context.Context, deployments tlsDeployments, service string, certificateRoutes ...tlsDeployments) error {
	phase := "TLS与协议就绪检查"
	if len(certificateRoutes) > 0 {
		phase = "证书准备"
	}
	frontends := deployments[service].Frontends
	fallback := map[string]bool{}
	primary, cancel := context.WithTimeout(ctx, tlsIssuerBudget)
	var err error
	for _, f := range frontends {
		if len(certificateRoutes) > 0 && f.Mode != "caddy" {
			continue
		}
		if len(certificateRoutes) > 0 {
			err = m.waitStoredCertificate(primary, f.Domain)
		} else {
			err = probeFrontendTLS(primary, f)
		}
		if err != nil {
			break
		}
	}
	cancel()
	if err == nil {
		return nil
	}
	for _, f := range frontends {
		if f.Mode == "caddy" && f.ACMEEmail != "" {
			fallback[f.Domain] = true
		}
	}
	if len(fallback) == 0 || ctx.Err() != nil {
		return tlsPhaseError(phase, err)
	}
	if err = m.syncWithFallback(ctx, deployments, fallback, certificateRoutes...); err != nil {
		return err
	}
	backup, cancel := context.WithTimeout(ctx, tlsIssuerBudget)
	defer cancel()
	for _, f := range frontends {
		if len(certificateRoutes) > 0 && f.Mode != "caddy" {
			continue
		}
		if len(certificateRoutes) > 0 {
			err = m.waitStoredCertificate(backup, f.Domain)
		} else {
			err = probeFrontendTLS(backup, f)
		}
		if err != nil {
			return tlsPhaseError(phase, fmt.Errorf("备用机构等待失败：%w", err))
		}
	}
	// Restore the normal issuer chain for renewal. CertMagic discovers and
	// reuses certificates from either issuer in the unchanged storage directory.
	return m.syncWithFallback(ctx, deployments, nil, certificateRoutes...)
}
