package serverconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const accountingPrefix = "qch-trf-"

type AccountingPort struct {
	Port      int      `json:"port"`
	Inbound   string   `json:"inbound"`
	Outbounds []string `json:"outbounds,omitempty"`
	Mark      uint32   `json:"mark,omitempty"`
}

type AccountingPlan struct {
	Content string           `json:"-"`
	Source  string           `json:"source"`
	API     string           `json:"api,omitempty"`
	Ports   []AccountingPort `json:"ports"`
}

func accountingTag(port int, original string) string {
	sum := sha256.Sum256([]byte(original))
	return fmt.Sprintf("%s%d-%s", accountingPrefix, port, hex.EncodeToString(sum[:6]))
}

// PrepareAccounting retains original routes and inserts equivalent,
// inbound-constrained routes to independent outbound instances. Unknown
// routing shapes fail closed, rather than silently bypassing a custom route.
func PrepareAccounting(engine core.Engine, content string) (AccountingPlan, error) {
	return prepareAccounting(engine, content, false)
}

func prepareAccounting(engine core.Engine, content string, forceMarks bool) (AccountingPlan, error) {
	root, err := decodeAccountingRoot(engine, content)
	if err != nil {
		return AccountingPlan{}, err
	}
	plan := AccountingPlan{Source: "core-api"}
	if engine == core.EngineShadowsocksRust {
		plan.Source = "nft-dual"
		if root["plugin"] != nil {
			return plan, fmt.Errorf("SS Rust plugin transport requires explicit accounting review")
		}
		entries, _ := root["servers"].([]any)
		if root["servers"] != nil && root["server_port"] != nil {
			return plan, fmt.Errorf("SS Rust accounting cannot combine root and server-array listeners")
		}
		for _, key := range []string{"manager_address", "local_address", "local_port"} {
			if root[key] != nil {
				return plan, fmt.Errorf("SS Rust %s can bypass fixed independent exits", key)
			}
		}
		if entries == nil {
			entries = []any{root}
		}
		seen := map[int]bool{}
		for _, raw := range entries {
			entry, _ := raw.(map[string]any)
			if entry["plugin"] != nil {
				return plan, fmt.Errorf("SS Rust plugin transport requires explicit accounting review")
			}
			port := trafficPortNumber(entry["server_port"])
			if port == 0 || seen[port] {
				return plan, fmt.Errorf("SS Rust accounting requires explicit server_port")
			}
			seen[port] = true
			mark := uint32(0x51430000) | uint32(port)
			if existing := entry["outbound_fwmark"]; existing != nil && intValue(existing) != 0 && uint32(intValue(existing)) != mark {
				return plan, fmt.Errorf("port %d already has an outbound mark", port)
			}
			if intValue(root["outbound_fwmark"]) != 0 && entry != nil && len(entries) > 1 {
				return plan, fmt.Errorf("global outbound mark must be reviewed before per-port accounting")
			}
			entry["outbound_fwmark"] = mark
			plan.Ports = append(plan.Ports, AccountingPort{Port: port, Mark: mark})
		}
	} else if engine == core.EngineMihomo {
		return prepareMihomoAccounting(root)
	} else if engine == core.EngineXray || engine == core.EngineSingBox {
		// sing-box server endpoints live outside `inbounds` and therefore do
		// not expose the core API counters used by tagged accounting. Their
		// fixed listener port can still be isolated at the network layer; keep
		// the native endpoint untouched and report the accounting source
		// explicitly instead of pretending it has core counters.
		if engine == core.EngineSingBox && root["endpoints"] != nil {
			inbounds, _ := root["inbounds"].([]any)
			if len(inbounds) == 0 {
				entries, err := DiscoverSingBoxEntries(content)
				if err != nil {
					return plan, err
				}
				plan.Source = "nft-dual"
				seen := map[int]bool{}
				for _, entry := range entries {
					if entry.Section != "endpoints" || entry.Port == 0 || seen[entry.Port] || entry.Port == 10085 || entry.Port == 10086 {
						return plan, fmt.Errorf("sing-box endpoint accounting requires unique fixed listen ports")
					}
					seen[entry.Port] = true
					plan.Ports = append(plan.Ports, AccountingPort{Port: entry.Port, Inbound: entry.Tag, Mark: uint32(0x51430000) | uint32(entry.Port)})
				}
				if len(plan.Ports) == 0 {
					return plan, fmt.Errorf("no attributable endpoints")
				}
				plan.Content = content
				return plan, nil
			}
		}
		if err := prepareTaggedAccounting(engine, root, &plan, forceMarks); err != nil {
			return plan, err
		}
	} else {
		return plan, fmt.Errorf("unsupported accounting engine")
	}
	if len(plan.Ports) == 0 {
		return plan, fmt.Errorf("no attributable listeners")
	}
	value, err := json.MarshalIndent(root, "", "  ")
	plan.Content = string(value) + "\n"
	return plan, err
}
