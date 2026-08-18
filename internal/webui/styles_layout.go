package webui

// stylesLayout is the cross-page layout rhythm and responsive reading-order pass.
// Each const holds one contiguous slice of the stylesheet; the global order is
// reassembled in styles_desktop_app.go. Keep every const byte-identical to the
// corresponding line range of the original single-const layout.
const stylesLayout = `/* Cross-page layout rhythm and responsive reading order. */
.dashboard-head,.ops-stats,.dashboard-columns{width:100%;max-width:1240px;margin-left:auto;margin-right:auto}.dashboard-columns{grid-template-columns:minmax(0,1.45fr) minmax(340px,.75fr)}
.enrollment-sheet,.machine-stack{width:100%;max-width:1280px;margin-left:auto;margin-right:auto}.service-grid{grid-template-columns:repeat(auto-fit,minmax(290px,1fr))}
.machine-facts dt,.service-version small,.service-facts dt,.service-endpoint small,.runtime-drawer>summary small{font-size:9px}.machine-facts dd,.service-facts dd{font-size:11px}.identity-list dt,.identity-list dd,.telemetry-lines>div>span,.telemetry-lines>div>strong{font-size:10px}.machine-telemetry>small,.network-line small{font-size:9px}.engine-state b,.service-endpoint b,.runtime-drawer>summary b,.machine-footer b{font-size:10px}.service-version strong{font-size:12px}.deployment-drift,.service-endpoint code,.machine-footer small{font-size:9px}
.config-workspace,.live-config-workspace{width:100%;max-width:1240px;margin-left:auto;margin-right:auto}.live-config-editor{grid-template-columns:minmax(0,1fr) minmax(290px,320px)}.live-config-inspector dt{font-size:10px}.live-config-inspector dd{font-size:11px;line-height:1.55}.live-config-note{font-size:10px}
.task-workspace{width:100%;max-width:1180px;margin-left:auto;margin-right:auto}.task-event-body{grid-template-columns:minmax(0,1fr) auto;align-items:center}.event-result{justify-self:end}.event-action strong{font-size:13px}.event-action small,.task-event time small,.event-target>span b,.event-target>span small{font-size:9px}.task-event time b,.audit-live,.event-result{font-size:10px}
.workspace-panel>header>a{font-size:11px}.fleet-overview-list strong,.recent-tasks strong{font-size:12px}.fleet-overview-list small,.fleet-overview-list time,.recent-tasks small,.recent-tasks time{font-size:10px}
@media(max-width:1100px){.dashboard-columns{grid-template-columns:1fr}.live-config-editor{grid-template-columns:1fr}.task-event-body{grid-template-columns:1fr}.event-result{justify-self:start}}
@media(max-width:820px){.dashboard-head{display:grid;grid-template-columns:minmax(0,1fr) auto;align-items:start;gap:12px}.dashboard-head>.trust-badge{margin-top:22px}.machine-body{display:flex;flex-direction:column}.service-canvas{order:1;padding:14px}.machine-profile{order:2;border-top:1px solid var(--line);border-bottom:0}.machine-footer{order:3}.service-grid{grid-template-columns:1fr}.live-config-inspector dl{display:grid;grid-template-columns:1fr 1fr;gap:0 14px}.live-config-inspector dl>div{display:block}.live-config-inspector dl>div:nth-child(3){grid-column:1/-1}.live-config-inspector dt{margin-bottom:3px}.task-event-body{gap:10px}.event-result{justify-self:start}}
@media(max-width:520px){.dashboard-head{grid-template-columns:minmax(0,1fr) auto}.dashboard-head h2{font-size:21px}.dashboard-head>div>p:last-child{max-width:250px}.dashboard-head>.trust-badge{margin-top:21px;padding:6px 8px}}

`
