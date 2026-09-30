// Package accountidentity holds optional display metadata, never credential or
// authorization identity. Stable profile names remain the credential keys.
package accountidentity

import (
	"encoding/json"
	"strings"
	"unicode"
)

type Identity struct {
	Email string `json:"email,omitempty" yaml:"email,omitempty"`
	Name  string `json:"name,omitempty" yaml:"name,omitempty"`
}

// Clean strips terminal controls and formatting characters and bounds labels.
func Clean(s string) string {
	var out []rune
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		out = append(out, r)
		if len(out) == 254 {
			break
		}
	}
	return strings.TrimSpace(string(out))
}
func (i Identity) Normalized() Identity { return Identity{Email: Clean(i.Email), Name: Clean(i.Name)} }
func (i Identity) Display() string {
	i = i.Normalized()
	if i.Email != "" {
		return i.Email
	}
	return i.Name
}

// Decode is deliberately forgiving: malformed optional identity must not make
// an otherwise successful token exchange fail.
func Decode(raw json.RawMessage) Identity {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return Identity{}
	}
	read := func(key string) string { var s string; _ = json.Unmarshal(fields[key], &s); return s }
	email := read("email")
	if email == "" {
		email = read("email_address")
	}
	name := read("name")
	if name == "" {
		name = read("display_name")
	}
	return (Identity{Email: email, Name: name}).Normalized()
}
