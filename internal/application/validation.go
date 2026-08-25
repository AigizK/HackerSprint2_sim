package application

import "regexp"

var (
	requestIDPattern    = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	agentIDPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	agentVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	runIDPattern        = regexp.MustCompile(`^[A-Za-z0-9]{20,64}$`)
)

func ValidRequestID(value string) bool    { return requestIDPattern.MatchString(value) }
func ValidAgentID(value string) bool      { return agentIDPattern.MatchString(value) }
func ValidAgentVersion(value string) bool { return agentVersionPattern.MatchString(value) }
func ValidRunID(value string) bool        { return runIDPattern.MatchString(value) }
