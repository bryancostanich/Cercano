package agentclient

import "cercano/source/server/pkg/accountidentity"

// AccountLabel never changes the profile ID used by actions or credentials.
// Keep the configured name visible even when multiple accounts share an email.
func (p CloudProfileInfo) AccountLabel(provider string) string {
	identity := (accountidentity.Identity{Email: p.AccountEmail, Name: p.AccountDisplayName}).Display()
	label := accountidentity.Clean(p.Name)
	if identity != "" {
		label = identity + " (" + label + ")"
	}
	if provider != "" {
		if p.Route == "chatgpt" {
			provider = "ChatGPT"
		} else if p.Route == "subscription" && p.Flavor == "messages" {
			provider = "Claude"
		}
		return accountidentity.Clean(provider) + " — " + label
	}
	return label
}
