package webui

// desktopAppStyles is the standalone desktop-workstation visual system.
//
// Split by theme into the styles_*.go files; the concatenation order below
// mirrors the original single-const byte layout, so the served CSS is unchanged.
const desktopAppStyles = stylesTokens +
	stylesBase +
	stylesShell +
	stylesDashboard +
	stylesAgents +
	stylesConfig +
	stylesTasks +
	stylesBuilder +
	stylesClientLogin +
	stylesResponsive +
	stylesLayout +
	stylesTrust +
	stylesSettings +
	stylesTasksBatching +
	stylesNodeWorkspace +
	stylesNodeConfig +
	stylesNodeResources +
	stylesNodeEnrollment +
	stylesWideConfig +
	stylesHierarchy +
	stylesTemplates +
	stylesClientAccessDrawer +
	stylesClientAccessPage +
	stylesCopyReduction +
	stylesGptImageV45 +
	stylesPolishV46 +
	stylesPolishV47
