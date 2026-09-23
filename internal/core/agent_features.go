package core

// AgentFeatureSelfUpgrade identifies the signed task protocol needed for a
// running Agent to replace its own executable and reconnect. Older Agents do
// not advertise this feature and must be reinstalled once before remote
// upgrades can be used.
const AgentFeatureSelfUpgrade = "agent-self-upgrade-v1"

// AgentFeaturePortTraffic identifies Agents that can account and enforce
// per-port traffic quotas independently of the managed proxy engine.
const AgentFeaturePortTraffic = "port-traffic-v1"

// AgentFeatureCoreLogs identifies Agents that stream managed core logs to the
// control plane without persisting them on the node.
const AgentFeatureCoreLogs = "core-logs-v1"

// AgentFeatureCoreLogStatus identifies Agents that report the per-engine
// health of their managed log source. Older Agents continue to stream logs,
// but the panel cannot distinguish an idle source from a failed collector.
const AgentFeatureCoreLogStatus = "core-log-status-v1"

// AgentFeatureMihomoDevelopmentSource identifies Agents that understand the
// negotiated Mihomo development source. Older Agents ignore an unknown
// core_source field during JSON/WSS decoding and would silently fall back to
// the official repository, so the control plane must refuse a mirror task for
// an Agent that does not advertise this feature.
const AgentFeatureMihomoDevelopmentSource = "mihomo-development-source-v1"

// AgentFeaturePublicIPProbe identifies Agents that actively probe their public
// IPv4 and IPv6 egress addresses and report both families in metrics.
const AgentFeaturePublicIPProbe = "public-ip-probe-v1"

// AgentFeatureManagedPublicIPProbe identifies Agents that can receive an
// operator-supplied probe configuration over the authenticated WSS session.
// The control plane sends that configuration only after the current session's
// first complete heartbeat advertises this feature.
const AgentFeatureManagedPublicIPProbe = "managed-public-ip-probe-v1"

// AgentFeatureManagedPolicy identifies Agents that can apply reporting and
// local core-log retention policy updates without a reinstall or restart.
const AgentFeatureManagedPolicy = "managed-agent-policy-v1"

// AgentFeatureManagedConfigRead identifies Agents that can independently read
// the QAgent-managed configuration while an external service is also exposed
// as an optional import source.
const AgentFeatureManagedConfigRead = "managed-config-read-v1"

// AgentFeatureConfigFiles advertises validated, rollback-safe materialization
// of Xray/sing-box source fragments alongside the runtime configuration.
const AgentFeatureConfigFiles = "config-files-v1"

// AgentFeaturePairedConfigFiles keeps an inbound and its dedicated exits in
// the same immutable source fragment (sources-v3 layout).
const AgentFeaturePairedConfigFiles = "config-files-paired-v1"

// AgentFeaturePresetAutoInstall negotiates install-if-missing configuration
// tasks. Older Agents would silently ignore the flag and must not receive one.
const AgentFeaturePresetAutoInstall = "preset-auto-install-v1"

const AgentFeatureSystemBBR = "system-bbr-v1"

// Older Agents may fall back to the original config when accounting cannot
// be compiled. Configuration validation/deployment must negotiate fail-closed
// independent egress before dispatch.
const AgentFeatureIndependentEgress = "independent-egress-v1"

// AgentFeatureSharedCoreInstances means each accepted share deploys to its
// own managed service and configuration path.
const AgentFeatureSharedCoreInstances = "shared-core-instances-v1"
