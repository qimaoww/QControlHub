package serverconfig

import _ "embed"

// StaticPage is a bundled, self-contained page. Selection copies its HTML into
// the versioned inbound, so a panel upgrade cannot change a deployed page.
type StaticPage struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	HTML string `json:"html"`
}

//go:embed staticpages/cleanfolio.html
var cleanfolioPage string

//go:embed staticpages/minimalio.html
var minimalioPage string

//go:embed staticpages/uisual-line.html
var uisualLinePage string

func xhttpStaticPages() []StaticPage {
	return []StaticPage{{"cleanfolio", "Cleanfolio 个人主页", cleanfolioPage}, {"minimalio", "Minimalio 博客", minimalioPage}, {"uisual-line", "Uisual Line 产品展示", uisualLinePage}}
}
