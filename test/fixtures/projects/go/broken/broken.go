package broken

// Deliberate compile error: undefined identifier.
func Boom() int { return undefined_identifier }
