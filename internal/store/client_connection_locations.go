package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/geoip"
)

type ClientConnectionLocation struct {
	core.ClientIPLocation
	IP         string    `json:"ip"`
	RetryAfter time.Time `json:"retry_after"`
}

// These cache APIs are internal. Callers derive IPs only from an authorized
// connection-history response; the cache never exposes a public IP lookup API.
func (s *Store) ClientConnectionLocations(ctx context.Context, ips []string) (map[string]ClientConnectionLocation, error) {
	result := make(map[string]ClientConnectionLocation)
	rows, err := s.pool.Query(ctx, `SELECT host(client_ip),country_code,country,province,retry_after FROM client_connection_locations WHERE client_ip=ANY($1::inet[])`, ips)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ClientConnectionLocation
		if err := rows.Scan(&item.IP, &item.CountryCode, &item.Country, &item.Province, &item.RetryAfter); err != nil {
			return nil, err
		}
		result[item.IP] = item
	}
	return result, rows.Err()
}

func (s *Store) SaveClientConnectionLocations(ctx context.Context, items []ClientConnectionLocation) error {
	if len(items) == 0 {
		return nil
	}
	if len(items) > 200 {
		return fmt.Errorf("%w: too many IP locations", ErrInvalid)
	}
	for _, item := range items {
		ip, err := netip.ParseAddr(item.IP)
		if err != nil || ip.Zone() != "" || item.RetryAfter.IsZero() || (item.CountryCode != "" && !geoip.ValidRegionCode(item.CountryCode)) || len(item.Country) > 200 || len(item.Province) > 100 || !utf8.ValidString(item.Country+item.Province) || strings.ContainsRune(item.Country+item.Province, '\x00') || (item.CountryCode != "CN" && item.Province != "") {
			return fmt.Errorf("%w: invalid IP location", ErrInvalid)
		}
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO client_connection_locations(client_ip,country_code,country,province,retry_after)
 SELECT ip::inet,COALESCE(country_code,''),COALESCE(country,''),COALESCE(province,''),retry_after
 FROM jsonb_to_recordset($1::jsonb) AS x(ip text,country_code text,country text,province text,retry_after timestamptz)
 ON CONFLICT(client_ip) DO UPDATE SET
 country_code=CASE WHEN EXCLUDED.country_code='' THEN client_connection_locations.country_code ELSE EXCLUDED.country_code END,
 country=CASE WHEN EXCLUDED.country_code='' THEN client_connection_locations.country ELSE EXCLUDED.country END,
 province=CASE WHEN EXCLUDED.country_code='' THEN client_connection_locations.province ELSE EXCLUDED.province END,
 retry_after=EXCLUDED.retry_after`, payload)
	return err
}

func (s *Store) PruneClientConnectionLocations(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM client_connection_locations location WHERE NOT EXISTS(SELECT 1 FROM client_connections c WHERE c.client_ip=location.client_ip)`)
	return err
}
