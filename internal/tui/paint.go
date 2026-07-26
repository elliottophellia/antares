package tui

import "encoding/base64"

// base64Encode is used by the OSC 52 clipboard escape.
func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
