package webui

// stylesTrust is the Corporate Trust v30 full-workflow refinement pass; stylesPolishV46 is the v46 polish pass; stylesPolishV47 is the v47 readability and micro-interaction pass.
// Each const holds one contiguous slice of the stylesheet; the global order is
// reassembled in styles_desktop_app.go. Keep every const byte-identical to the
// corresponding line range of the original single-const layout.
const stylesTrust = `/* Corporate Trust v30: full-workflow refinements guided by visual audits. */
.desktop-app{grid-template-columns:88px 250px minmax(0,1fr);background:var(--canvas)}
.app-dock{margin:12px;padding:12px 5px;border:1px solid color-mix(in srgb,var(--line) 20%,transparent);border-radius:20px;box-shadow:0 26px 64px -34px rgba(7,9,18,.92)}
.dock-logo{width:44px;height:44px;border-radius:14px;background:linear-gradient(145deg,#fff,#e8e9f3);box-shadow:0 10px 24px -16px rgba(0,0,0,.65)}
.dock-nav{margin-top:24px;gap:8px}.dock-nav a,.dock-tools button{width:48px;min-height:48px;border-radius:13px}.dock-nav a.active{background:linear-gradient(145deg,#fff,#efefff);box-shadow:0 8px 24px -16px rgba(87,85,231,.9)}
.context-sidebar{margin:12px 0;border:1px solid var(--line);border-radius:18px;background:color-mix(in srgb,var(--surface) 92%,var(--canvas));box-shadow:var(--shadow);scrollbar-width:thin}
.context-brand{min-height:68px;padding:12px 16px;border-bottom-color:var(--line)}.brand-mark{border-radius:11px;background:linear-gradient(145deg,var(--accent),#7067f1);box-shadow:0 10px 22px -14px rgba(87,85,231,.9)}
.context-heading{padding:24px 18px 16px}.context-heading h1{margin-top:6px;font-size:28px;letter-spacing:-.035em}.context-heading>span{max-width:210px;font-size:11px;line-height:1.65}
.context-primary{margin:0 14px 18px;border-radius:11px;background:linear-gradient(135deg,var(--accent),#6c56dc);box-shadow:0 12px 24px -16px rgba(87,85,231,.92)}
.context-menu{gap:5px;padding:0 10px}.context-menu a{border-radius:11px}.context-menu a:hover,.context-menu a.active,.context-list>a:hover,.context-list>a.active{box-shadow:none}.context-menu a.active,.context-list>a.active{background:var(--accent-soft);color:var(--ink);box-shadow:inset 3px 0 0 var(--accent)}
.context-list{gap:5px;padding:0 10px}.context-list>a{border:1px solid transparent;border-radius:12px}.context-list>a:hover{border-color:var(--line);background:var(--surface)}.context-list>a.active small{color:var(--ink-2)}
.context-metrics{gap:0;margin:20px 14px 0;border:0;border-radius:13px;background:var(--surface-3)}.context-metrics div{padding:11px 12px;border-bottom:1px solid var(--line)}.context-metrics div:last-child{border-bottom:0}
.workspace-shell{position:relative;background:radial-gradient(circle at 72% -20%,color-mix(in srgb,var(--accent) 9%,transparent),transparent 40%),var(--canvas)}
.workspace-topbar{height:58px;margin:12px 20px 0;padding:0 18px;border:1px solid var(--line);border-radius:14px;background:var(--surface);box-shadow:var(--shadow)}
.workspace-main{padding:24px 28px 32px}.workspace-route{font-weight:600}.workspace-route b{letter-spacing:-.01em}
.button,input,select,textarea{border-radius:var(--radius-control)}.button{border-color:var(--line-2)}.button.primary{border-color:transparent;background:linear-gradient(135deg,var(--accent),#6d56df);box-shadow:0 12px 24px -16px rgba(87,85,231,.92)}.button.primary:hover{background:linear-gradient(135deg,var(--accent-2),#5b45c7);box-shadow:0 16px 28px -18px rgba(87,85,231,.95)}
.dashboard-head h2{font-size:27px;letter-spacing:-.035em}.dashboard-head>div>p:last-child{font-size:12px}.trust-badge{padding:7px 11px;border:1px solid color-mix(in srgb,var(--green) 18%,transparent);box-shadow:0 8px 24px -20px color-mix(in srgb,var(--green) 70%,transparent)}
.ops-stats{grid-template-columns:repeat(6,minmax(0,1fr));gap:12px}.ops-stats>a{grid-column:span 1;min-height:118px;display:flex;align-items:flex-start;flex-direction:column;justify-content:space-between;gap:14px;padding:17px;border-color:transparent;border-radius:var(--radius-card);box-shadow:var(--shadow);transition:transform .2s ease,box-shadow .2s ease}.ops-stats>a:first-child{grid-column:span 3;display:grid;grid-template-columns:52px minmax(0,1fr);align-items:center;min-height:150px;padding:24px;background:linear-gradient(135deg,var(--surface),color-mix(in srgb,var(--accent-soft) 58%,var(--surface)))}.ops-stats>a:nth-child(n+2){background:color-mix(in srgb,var(--surface) 95%,var(--canvas))}.ops-stats>a:hover{transform:translateY(-3px);box-shadow:var(--shadow-lift)}.ops-stats>a:first-child .stat-icon{width:52px;height:52px;border-radius:16px}.ops-stats>a:first-child strong{font-size:36px}.ops-stats>a:first-child p{font-size:10px}.ops-stats strong{font-size:27px;letter-spacing:-.04em}.stat-icon{border-radius:12px}
.dashboard-columns{gap:14px;margin-top:14px}.workspace-panel{border-color:transparent;border-radius:var(--radius-card);box-shadow:var(--shadow)}.workspace-panel>header{padding:15px 18px;border-bottom-color:var(--line)}.workspace-panel h3{font-size:18px;letter-spacing:-.025em}.fleet-overview-list>a,.recent-tasks>div>a{margin:0 8px;border-bottom-color:var(--line);border-radius:10px}.fleet-overview-list>a:hover,.recent-tasks>div>a:hover{background:var(--accent-soft)}.node-avatar,.machine-avatar{border-radius:12px;background:linear-gradient(145deg,var(--accent-soft),color-mix(in srgb,var(--accent) 14%,var(--surface)));box-shadow:inset 0 0 0 1px color-mix(in srgb,var(--accent) 9%,transparent)}
.enrollment-sheet,.machine-workspace,.config-workspace,.live-config-workspace,.task-filter-panel,.task-event-card,.protocol-browser,.recipe-workspace,.client-access,.node-revision-timeline,.advanced-studio,.inbound-browser{border-color:transparent;border-radius:var(--radius-card);box-shadow:var(--shadow)}
.machine-stack{gap:16px}.machine-header{min-height:74px;padding:12px 17px}.machine-body{background:color-mix(in srgb,var(--surface-2) 88%,var(--canvas))}.machine-profile{padding:18px}.service-canvas{padding:18px}.service-card{border-color:var(--line);border-radius:13px;box-shadow:0 10px 24px -24px rgba(79,70,229,.55)}.service-card:hover{border-color:color-mix(in srgb,var(--accent) 28%,var(--line));box-shadow:var(--shadow-lift)}
.service-version strong{min-height:34px;display:-webkit-box;overflow:hidden;line-height:1.5;white-space:normal;-webkit-box-orient:vertical;-webkit-line-clamp:2}
.task-event{margin-bottom:12px}.task-event-card{border:1px solid color-mix(in srgb,var(--line) 80%,transparent)}.task-event.focused .task-event-card{outline:0;border-color:var(--accent);background:linear-gradient(135deg,var(--accent-soft),var(--surface));box-shadow:0 18px 42px -28px rgba(87,85,231,.65)}.task-event-card>header{padding:14px 16px}.task-event-body{padding:14px 16px}.task-timeline:before{background:linear-gradient(var(--accent),var(--line-2) 20%,var(--line-2) 80%,transparent)}
.editor-toolbar{min-height:78px;padding:15px 18px}.editor-toolbar h2{font-size:21px;letter-spacing:-.025em}.code-workspace>.code-editor-toolbar{min-height:62px;padding:11px 16px}.code-editor-frame{background:var(--code-canvas)}.code-gutter{border-right-color:var(--code-line);background:var(--code-gutter);color:var(--code-muted)}.code-editor-input{background:var(--code-canvas);color:var(--code-ink);caret-color:var(--code-caret)}.code-editor-input::selection{background:var(--code-selection);color:var(--code-ink)}.code-editor-input:focus{background:var(--code-canvas)}.code-language{border-color:#dfe3ef;border-radius:7px}.live-config-inspector,.config-inspector{background:linear-gradient(180deg,var(--surface-2),var(--surface))}
.recipe-toolbar{min-height:78px;padding:14px 17px}.recipe-icon,.section-number{border-radius:11px}.builder-index{background:color-mix(in srgb,var(--accent-soft) 38%,var(--surface-2))}.builder-index a{border-radius:10px}.builder-section{padding:20px}.client-share{border-color:color-mix(in srgb,var(--accent) 22%,var(--line));border-radius:12px;background:linear-gradient(135deg,var(--accent-soft),var(--surface))}
.advanced-studio>summary{min-height:70px;padding:13px 16px;cursor:pointer}.advanced-studio>summary b{font-size:13px}.advanced-studio>summary small,.advanced-studio>summary i{font-size:10px}.advanced-studio-body{grid-template-columns:220px minmax(0,1fr) 210px}.field-rail>header,.official-rail>header{padding:12px 13px;font-size:10px}.field-rail>a{gap:8px;padding:9px 11px;border-left:3px solid transparent}.field-rail>a:hover{background:var(--surface-2)}.field-rail>a.active{border-left-color:var(--accent);background:var(--accent-soft)}.field-rail strong{font-size:10px}.field-rail code,.field-rail small{font-size:9px}.field-canvas{padding:18px}.field-canvas h2{font-size:20px;letter-spacing:-.025em}.field-canvas header code,.field-canvas header a,.field-canvas>p{font-size:10px}.field-canvas>p{margin:10px 0 14px;line-height:1.65}.field-canvas form textarea{min-height:250px;line-height:1.6}.field-canvas form>footer,.source-studio form>footer{align-items:center;margin-top:10px}.field-canvas form>footer>span,.source-studio form>footer>span{color:var(--muted);font-size:10px}.source-studio{margin-top:14px}.source-studio>summary{padding:12px 0;font-size:11px;font-weight:700}.source-studio form>textarea{min-height:420px;background:var(--code-canvas);color:var(--code-ink);caret-color:var(--code-caret)}.source-studio form>textarea::selection{background:var(--code-selection);color:var(--code-ink)}.official-rail>p,.official-rail summary{padding:10px 12px;font-size:10px}.official-rail details>div{gap:5px;padding:7px}.official-rail details>div a{padding:7px;border-radius:6px;font-size:9px}
.enrollment-security-note{display:flex;align-items:flex-start;gap:9px;margin-top:10px;padding:10px 12px;border:1px solid color-mix(in srgb,var(--accent) 20%,var(--line));border-radius:10px;background:var(--accent-soft);color:var(--ink-2);font-size:9px}.enrollment-security-note b{flex:0 0 auto;color:var(--accent)}
.task-event-card:has(.event-result details[open]) .task-event-body{grid-template-columns:1fr}.task-event-card:has(.event-result details[open]) .event-result{width:100%;justify-self:stretch}.event-result details[open]{width:100%}.event-result details[open]>summary{margin-bottom:8px}.task-result-block{overflow:hidden;border:1px solid var(--line);border-radius:11px;background:var(--surface)}.task-result-block>header{min-height:42px;margin:0;padding:0 11px;border-bottom:1px solid var(--line);background:var(--surface-2)}.event-result .task-result-block pre{max-height:360px;padding:13px;border:0;border-radius:0;background:#111827;color:#e6ebf5;font-size:10px;line-height:1.65}.event-result .task-result-block pre.task-error{background:color-mix(in srgb,#111827 88%,var(--red));color:#ffd8de}
@media(max-width:1100px){.desktop-app{grid-template-columns:82px 226px minmax(0,1fr)}.ops-stats{grid-template-columns:repeat(3,1fr)}.ops-stats>a:first-child{grid-column:1/-1}.ops-stats>a:nth-child(n+2){grid-column:span 1}}
@media(max-width:820px){.desktop-app{height:auto;display:block;background:var(--canvas);overflow:visible}.app-dock{margin:0}.context-sidebar{margin:0;border:0;border-bottom:1px solid var(--line);border-radius:0;box-shadow:none;background:var(--surface-2)}.context-brand{min-height:60px}.context-heading{padding:18px 14px 13px}.context-heading h1{font-size:25px}.context-heading>span{max-width:none}.workspace-topbar{top:0;height:52px;margin:0;padding:0 12px;border-width:0 0 1px;border-radius:0;box-shadow:none}.workspace-main{padding:14px 12px 24px}.ops-stats{grid-template-columns:1fr 1fr;gap:10px}.ops-stats>a:first-child{grid-column:1/-1;min-height:126px;padding:18px}.ops-stats>a:nth-child(n+2){grid-column:span 1;min-height:112px;padding:14px}.dashboard-head h2{font-size:24px}.workspace-panel,.enrollment-sheet,.machine-workspace,.config-workspace,.live-config-workspace,.task-filter-panel,.task-event-card,.protocol-browser,.recipe-workspace,.client-access,.node-revision-timeline,.advanced-studio,.inbound-browser{border-radius:14px}.code-editor-frame,.code-editor-input{min-height:520px}}
@media(max-width:520px){.ops-stats{grid-template-columns:repeat(3,minmax(0,1fr));gap:8px}.ops-stats>a:first-child{grid-template-columns:46px minmax(0,1fr)}.ops-stats>a:first-child .stat-icon{width:46px;height:46px}.ops-stats>a:first-child strong{font-size:32px}.ops-stats>a:nth-child(n+2){grid-column:span 1;min-height:108px;padding:12px 10px}.ops-stats>a:nth-child(n+2) .stat-icon{width:38px;height:38px}.ops-stats>a:nth-child(n+2) strong{font-size:25px}.ops-stats>a:nth-child(n+2) p{display:none}.service-card{border-radius:12px}.builder-section{padding:16px 14px}}
@media(max-width:820px){
  .context-brand,.context-heading,.context-footer{display:none}
  .page-dashboard .context-sidebar{display:none}
  .context-sidebar{padding:10px 12px;overflow:visible}
  .context-primary{min-height:44px;margin:0 0 8px;padding:0 14px}
  .context-section-label{min-height:24px;padding:0 2px 5px}
  .context-list{display:flex;gap:8px;padding:0;overflow-x:auto;overscroll-behavior-inline:contain;scrollbar-width:none;scroll-snap-type:x proximity}
  .context-list::-webkit-scrollbar{display:none}
  .context-list>a{min-width:210px;min-height:50px;flex:0 0 auto;padding:7px 10px;scroll-snap-align:start}
  .config-context-list>a,.engine-context-list>a{min-width:168px}
  .context-section-label:not(:first-of-type){margin-top:8px}
  .task-context-menu{display:flex;gap:7px;padding:0;overflow-x:auto;scrollbar-width:none}
  .task-context-menu::-webkit-scrollbar{display:none}
  .task-context-menu a{min-width:max-content;min-height:42px;flex:0 0 auto;padding:0 14px}
  .context-callout,.context-steps,.context-back{display:none}
  .page-agents .context-primary{display:none}
  .page-tasks .context-sidebar{padding-block:8px}
  .page-agent-config .context-sidebar{padding-top:10px}
  .workspace-route span,.workspace-route i{display:none}
  .workspace-topbar{position:sticky;z-index:50}
  .sync-state span{display:none}
  .advanced-studio-body{display:block}
  .field-rail{display:flex;overflow-x:auto;overscroll-behavior-inline:contain;scrollbar-width:none;scroll-snap-type:x proximity}
  .field-rail::-webkit-scrollbar{display:none}
  .field-rail>header{min-width:112px;flex:0 0 112px;align-items:flex-start;flex-direction:column;justify-content:center}
  .field-rail>a{min-width:168px;min-height:66px;flex:0 0 168px;align-items:center;border-left:0;border-bottom:3px solid transparent;scroll-snap-align:start}
  .field-rail>a.active{border-bottom-color:var(--accent)}
  .field-canvas{padding:16px 14px}
  .field-canvas>header{gap:10px;align-items:flex-start;flex-direction:column}
  .field-canvas form>footer,.source-studio form>footer{align-items:stretch;flex-direction:column;gap:10px}
  .field-canvas form>footer div,.source-studio form>footer div{display:grid;grid-template-columns:1fr 1fr}
  .official-rail{border-left:0;border-top:1px solid var(--line)}
  .builder-actions{bottom:calc(76px + env(safe-area-inset-bottom));z-index:80;padding:9px 10px;border:1px solid var(--line);border-radius:12px 12px 0 0;box-shadow:0 -14px 34px -28px rgba(15,18,38,.7)}
  .builder-actions>div:first-child{display:none}
  .builder-actions>div:last-child{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:7px;width:100%}
  .builder-actions .button{min-width:0;padding:8px 6px;white-space:normal}
  .login-shell{background:#171922}
  .login-card{position:relative;margin-top:-18px;padding-top:40px;border-radius:24px 24px 0 0;background:var(--surface)}
  .timeline-body>nav{display:grid;gap:8px;padding:12px;border-right:0;border-bottom:1px solid var(--line);background:var(--surface-2)}
  .timeline-body>nav a{min-height:70px;padding:11px;border:1px solid var(--line);border-radius:11px;background:var(--surface)}
  .timeline-body>nav a.active{border-color:var(--accent);box-shadow:inset 3px 0 0 var(--accent)}
  .timeline-preview{padding:14px}
}
@media(max-width:520px){.ops-stats{grid-template-columns:repeat(3,minmax(0,1fr));gap:8px}.ops-stats>a:nth-child(n+2){grid-column:span 1;min-height:108px;padding:12px 10px}}

`

const stylesPolishV46 = `/* GPT Image reference v46: keep normal states self-explanatory and reserve copy for failures or confirmation. */
.role-badge{display:inline-flex;align-items:center;padding:2px 8px;border:1px solid var(--line);border-radius:999px;color:var(--muted);font-size:8px;font-style:normal;font-weight:750}.role-badge.role-admin{color:var(--accent);border-color:color-mix(in srgb,var(--accent) 32%,var(--line));background:var(--accent-soft)}.role-badge.role-operator{color:var(--amber);border-color:color-mix(in srgb,var(--amber) 32%,var(--line));background:var(--amber-soft)}.context-sidebar>.context-menu:first-of-type{margin-top:16px}.context-sidebar>.context-section-label:first-of-type{padding-top:16px}.context-sidebar>.context-primary{margin-top:16px}.dashboard-head{align-items:center}.dashboard-head>.trust-badge{margin-top:0}.workspace-panel>header{min-height:56px}.ops-stats>a,.ops-stats>a:first-child,.ops-stats>a:nth-child(n+2){min-height:88px}.settings-section>header{align-items:center}.settings-savebar{justify-content:flex-end}.live-config-inspector dl{margin-top:0}.node-config-source>h2{font-size:17px}.code-workspace>footer>span:has([data-code-validation]:empty){display:none}.field-canvas form>footer,.source-studio form>footer{justify-content:flex-end}.page-agent-config .config-mutation{display:flex;justify-content:flex-end}.page-agent-config .config-mutation>label{width:min(100%,310px)}
@media(min-width:821px){.page-dashboard .desktop-app{grid-template-columns:68px minmax(0,1fr)}.page-dashboard .context-sidebar{display:none}.page-agent-config .config-mutation{width:310px;margin:10px 12px 0 auto;padding:0;border:0;background:transparent}}
@media(max-width:820px){.dashboard-head{align-items:center;flex-direction:row}.dashboard-head>.trust-badge{margin-top:0}.ops-stats>a,.ops-stats>a:first-child,.ops-stats>a:nth-child(n+2){min-height:90px}.settings-hero{margin-bottom:10px}.page-agent-config .config-mutation>label{width:100%}}
@media(prefers-reduced-motion:reduce){.ops-stats>a{transition:none}.ops-stats>a:hover{transform:none}}
`

const stylesPolishV47 = `/* Polish V47: readability floor, micro-interactions, and missing shadow token. */
:root,[data-theme=light]{--shadow-hover:0 6px 20px rgba(34,39,68,.09),0 26px 58px -34px rgba(79,70,229,.52)}
[data-theme=dark]{--shadow-hover:0 14px 36px -22px rgba(0,0,0,.88),0 4px 12px rgba(0,0,0,.24)}
.service-client-access small,.service-client-access strong,.client-access-entry-meta small,.timeline-body>nav small,.event-action small,.task-event time small,.delivery-bar small,.config-danger small,.access-steps small,.client-share>header small,.client-parameters>div>span:first-child{font-size:9px}
.machine-resource-summary progress,.telemetry-lines progress{height:4px;border-radius:4px}
.machine-resource-summary progress::-webkit-progress-bar,.telemetry-lines progress::-webkit-progress-bar{border-radius:4px}
.machine-resource-summary progress::-webkit-progress-value,.telemetry-lines progress::-webkit-progress-value{border-radius:4px;transition:width .55s cubic-bezier(.25,.46,.45,.94)}
.service-card{transition:border-color .18s ease,box-shadow .18s ease,transform .15s ease}.page-agents .service-card:hover{transform:translateY(-1px)}
.context-primary{transition:background .15s ease,transform .12s ease,box-shadow .15s ease}.context-primary:hover{transform:translateY(-1px)}.context-primary:active{transform:translateY(0)}
.alert{border-radius:10px}.alert.success{box-shadow:inset 0 0 0 1px color-mix(in srgb,var(--green) 18%,transparent)}.alert.error{box-shadow:inset 0 0 0 1px color-mix(in srgb,var(--red) 18%,transparent)}
.code-editor-input{font-size:12px;line-height:1.7}.code-gutter{font-size:11px;line-height:1.7}
::placeholder{opacity:.62}
.timeline-marker>i{width:12px;height:12px;margin-top:16px}
.context-list>a{transition:background .14s ease,border-color .14s ease,color .14s ease}
@media(prefers-reduced-motion:reduce){.machine-resource-summary progress::-webkit-progress-value,.telemetry-lines progress::-webkit-progress-value{transition:none}.service-card,.context-primary,.context-list>a{transition:none}.page-agents .service-card:hover,.context-primary:hover{transform:none}}
`
