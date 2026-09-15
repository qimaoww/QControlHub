package store

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/geoip"
)

const komariUUIDLabel = "komari_uuid"

// AgentRegionCode returns the optional operator-selected display region.
func AgentRegionCode(agent core.Agent) string {
	code := strings.ToUpper(strings.TrimSpace(agent.Labels["region_code"]))
	if !geoip.ValidRegionCode(code) {
		return ""
	}
	return code
}

// SetAgentRegionCode changes only the region preference, preserving all other
// labels. Clearing it restores automatic GeoIP without requiring a migration.
func (s *Store) SetAgentRegionCode(ctx context.Context, id, code string) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code != "" && !geoip.ValidRegionCode(code) {
		return fmt.Errorf("%w: unsupported country/region code", ErrInvalid)
	}
	if err := requireAgentAdministration(ctx, s.pool, id); err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE agents SET labels = CASE
			WHEN $2='' THEN COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb) - 'region_code'
			ELSE jsonb_set(COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb), '{region_code}', to_jsonb($2::text), true)
		END
		WHERE id=$1 AND revoked_at IS NULL`, id, code)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAgentName changes only panel display metadata. Enrollment credentials
// keep their original names, and reconnects continue to use the stable ID/key.
func (s *Store) SetAgentName(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return fmt.Errorf("%w: agent name must contain 1 to 100 characters without control characters", ErrInvalid)
	}
	if err := requireAgentAdministration(ctx, s.pool, id); err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `UPDATE agents SET name=$2 WHERE id=$1 AND revoked_at IS NULL`, id, name)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AgentKomariUUID returns the optional Komari node UUID stored with the
// QControlHub agent's display labels. Keeping this as an agent preference
// avoids coupling the enrollment protocol to a third-party monitor.
func AgentKomariUUID(agent core.Agent) string {
	return strings.TrimSpace(agent.Labels[komariUUIDLabel])
}

// SetAgentKomariUUID updates or clears the optional Komari node UUID.
func (s *Store) SetAgentKomariUUID(ctx context.Context, id, uuid string) error {
	uuid = strings.TrimSpace(uuid)
	if len(uuid) > 100 || strings.ContainsAny(uuid, "\r\n\t") {
		return fmt.Errorf("%w: Komari server UUID is invalid", ErrInvalid)
	}
	if err := requireAgentAdministration(ctx, s.pool, id); err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE agents SET labels = CASE
			WHEN $2='' THEN COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb) - $3::text
			ELSE jsonb_set(COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb), ARRAY[$3::text], to_jsonb($2::text), true)
		END
		WHERE id=$1 AND revoked_at IS NULL`, id, uuid, komariUUIDLabel)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
