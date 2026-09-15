//go:build linux

package agent

import (
	"sort"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func coreLogJournalSources(specSets ...map[core.Engine]EngineSpec) []coreLogJournalSource {
	managed := make(map[string]core.Engine)
	generic := make(map[string]core.Engine)
	managedAmbiguous := make(map[string]bool)
	genericAmbiguous := make(map[string]bool)
	for _, specs := range specSets {
		for engine, spec := range specs {
			if managedCoreServiceName(spec.Service) {
				addCoreLogUnit(managed, managedAmbiguous, spec.Service, engine)
				// A project-managed fallback clears LogNamespace when namespaced
				// journald is unavailable. Follow the main journal in parallel;
				// only one source can contain a given managed unit entry.
				addCoreLogUnit(generic, genericAmbiguous, spec.Service, engine)
				continue
			}
			switch {
			case engine == core.EngineXray && spec.Service == "xray.service":
				addCoreLogUnit(generic, genericAmbiguous, spec.Service, engine)
			case engine == core.EngineSingBox && (spec.Service == "sing-box.service" || spec.Service == "singbox.service"):
				addCoreLogUnit(generic, genericAmbiguous, spec.Service, engine)
			case engine == core.EngineShadowsocksRust && spec.Service == "shadowsocks-rust.service":
				addCoreLogUnit(generic, genericAmbiguous, spec.Service, engine)
			}
		}
	}
	sources := make([]coreLogJournalSource, 0, 2)
	if len(managed) > 0 {
		sources = append(sources, newCoreLogJournalSource(managed, true))
	}
	if len(generic) > 0 {
		sources = append(sources, newCoreLogJournalSource(generic, false))
	}
	return sources
}

func addCoreLogUnit(units map[string]core.Engine, ambiguous map[string]bool, unit string, engine core.Engine) {
	if ambiguous[unit] {
		return
	}
	if existing, ok := units[unit]; ok && existing != engine {
		delete(units, unit)
		ambiguous[unit] = true
		return
	}
	units[unit] = engine
}

func newCoreLogJournalSource(units map[string]core.Engine, managedNamespace bool) coreLogJournalSource {
	arguments := []string{"--follow", "--lines=0", "--output=json",
		"--output-fields=MESSAGE,_SYSTEMD_UNIT,PRIORITY,__REALTIME_TIMESTAMP,__CURSOR"}
	if managedNamespace {
		arguments = append([]string{"--namespace=qagent-cores"}, arguments...)
	}
	names := make([]string, 0, len(units))
	copyMap := make(map[string]core.Engine, len(units))
	for unit, engine := range units {
		names = append(names, unit)
		copyMap[unit] = engine
	}
	sort.Strings(names)
	for _, unit := range names {
		arguments = append(arguments, "--unit="+unit)
	}
	return coreLogJournalSource{arguments: arguments, unitEngines: copyMap}
}
