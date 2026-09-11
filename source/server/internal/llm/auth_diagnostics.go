package llm

// SafeAuthenticationDiagnostic preserves the cause for programmatic inspection
// without printing provider response bodies that can reflect credentials.
func SafeAuthenticationDiagnostic(class ErrorClass, cause error) error {
	return &authenticationDiagnostic{class: class, cause: cause}
}

type authenticationDiagnostic struct {
	class ErrorClass
	cause error
}

func (e *authenticationDiagnostic) Error() string {
	if e.class == ErrPermission {
		return "provider denied access; check profile permissions"
	}
	return "provider rejected the configured credentials"
}
func (e *authenticationDiagnostic) Unwrap() error { return e.cause }
