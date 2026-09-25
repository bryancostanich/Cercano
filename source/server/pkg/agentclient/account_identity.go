package agentclient

import "cercano/source/server/pkg/accountidentity"

// AccountDisplayName is presentation only; Name remains the credential key.
func (p CloudProfileInfo) AccountDisplayNameLabel() string {
	if identity := (accountidentity.Identity{Email: p.AccountEmail, Name: p.AccountDisplayName}).Display(); identity != "" {
		return identity
	}
	if p.Route == "chatgpt" || p.Route == "subscription" {
		return "unidentified account (" + accountidentity.Clean(p.Name) + ")"
	}
	return accountidentity.Clean(p.Name)
}

func (p CloudProfileInfo) AccountLabel(provider string) string {
	label := p.AccountDisplayNameLabel()
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

// DistinctAccountLabel adds the internal ID only when another configured account
// would otherwise have the same label. Selection values always remain Name.
func (p CloudProfileInfo) DistinctAccountLabel(provider string, peers []CloudProfileInfo) string {
	label := p.AccountLabel(provider)
	for _, other := range peers {
		if other.Name != p.Name && other.AccountLabel(provider) == label {
			return label + " (" + accountidentity.Clean(p.Name) + ")"
		}
	}
	return label
}
