package main

import "os"

// daemonEnv reads the daemon's connection to the control plane and removes it
// from the process environment. Whatever coveyd starts later inherits that
// environment unless the starter filters it, and a filter forgotten in one
// plugin would hand the token on again; a variable that is gone cannot be.
func daemonEnv() (wsURL, token, agentID string) {
	wsURL = os.Getenv("COVEY_WS_URL")
	token = os.Getenv("COVEY_DAEMON_TOKEN")
	agentID = os.Getenv("COVEY_AGENT_ID")
	os.Unsetenv("COVEY_WS_URL")
	os.Unsetenv("COVEY_DAEMON_TOKEN")
	return wsURL, token, agentID
}
