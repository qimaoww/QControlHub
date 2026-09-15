package serverconfig

// ClientField is one exact value a client needs to reach a generated inbound.
// Secret fields must remain masked by callers until the administrator reveals
// or copies them.
type ClientField struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// ClientProfile contains a client import value plus the same connection data
// as explicit fields. Most protocols use a URI; protocols without a portable
// URI use their native single-line client configuration instead.
type ClientProfile struct {
	Format                 string        `json:"format"`
	URI                    string        `json:"uri"`
	Mihomo                 string        `json:"-"`
	MihomoError            string        `json:"-"`
	SubscriptionCompatible bool          `json:"subscription_compatible"`
	Fields                 []ClientField `json:"fields"`
}
