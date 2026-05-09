package persona

// ScopedSessionID prefixes a session ID with the persona ID for history isolation.
// When personaID is empty or "default", the original sessionID is returned unchanged.
func ScopedSessionID(personaID, sessionID string) string {
	if personaID == "" || personaID == "default" {
		return sessionID
	}
	return personaID + ":" + sessionID
}
