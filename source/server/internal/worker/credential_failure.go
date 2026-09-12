package worker

import (
	"context"
	"errors"

	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

func marshalCredentialFailure(err error, profile string) *proto.CredentialFailure {
	failure := &proto.CredentialFailure{Class: string(llm.ErrCredential), ProfileName: profile, Reason: llm.CredentialSource}
	if errors.Is(err, context.Canceled) {
		failure.Class = "canceled"
		return failure
	}
	if errors.Is(err, context.DeadlineExceeded) {
		failure.Class = "deadline"
		return failure
	}
	var credential *llm.CredentialError
	if errors.As(err, &credential) {
		failure.Class = string(credential.Class)
		failure.Provider = credential.Provider
		failure.Method = credential.Method
		failure.Reason = credential.Reason
	}
	return failure
}
func unmarshalCredentialFailure(f *proto.CredentialFailure, profile string) error {
	invalid := func() error {
		return &llm.CredentialError{Class: llm.ErrCredential, Profile: profile, Reason: "unsupported_error_metadata"}
	}
	if f == nil || f.GetProfileName() != profile {
		return invalid()
	}
	switch f.GetClass() {
	case "canceled":
		return context.Canceled
	case "deadline":
		return context.DeadlineExceeded
	}
	if f.GetProvider() != "" && f.GetProvider() != "anthropic" && f.GetProvider() != "openai-responses" {
		return invalid()
	}
	class := llm.ErrorClass(f.GetClass())
	switch class {
	case llm.ErrLoginRequired, llm.ErrCredential, llm.ErrPermission, llm.ErrNetwork, llm.ErrBusy:
	default:
		return invalid()
	}
	if class == llm.ErrLoginRequired && (f.GetMethod() != llm.AuthSubscription || f.GetProvider() == "") {
		return invalid()
	}
	// Reasons on this protocol are fixed codes, never arbitrary error text.
	switch f.GetReason() {
	case llm.CredentialMissing, llm.CredentialExpired, llm.CredentialRejected, llm.CredentialStore, llm.CredentialMalformed, llm.CredentialRefresh, llm.CredentialSource:
	default:
		return invalid()
	}
	return &llm.CredentialError{Class: class, Profile: profile, Provider: f.GetProvider(), Method: f.GetMethod(), Reason: f.GetReason()}
}
