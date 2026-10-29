// Package jforex adapts the authenticated Dukascopy JForex SDK bridge to the
// application's independent market-data interface. It never places orders.
package jforex

// Settings is a write-only request. An empty password retains an existing
// password only when the username and environment are unchanged.
type Settings struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	Environment   string `json:"environment"`
	ClearPassword bool   `json:"clearPassword"`
}

// Info deliberately excludes the saved password, including on failed writes.
type Info struct {
	Username           string `json:"username"`
	Environment        string `json:"environment"`
	PasswordConfigured bool   `json:"passwordConfigured"`
	RuntimeAvailable   bool   `json:"runtimeAvailable"`
	RuntimeDirectory   string `json:"runtimeDirectory"`
	JavaPath           string `json:"javaPath"`
	Connected          bool   `json:"connected"`
	InstrumentCount    int    `json:"instrumentCount"`
	Error              string `json:"error,omitempty"`
}

type LoginChallenge struct {
	ImageDataURL string `json:"imageDataUrl"`
}
