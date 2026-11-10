package teamsys

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// Name is the target system's name in ACCESS.md, guard-rail subjects and the
// store: `- system: teamsapp scope: read,write`.
const Name = "teamsapp"

// System is the agent-facing half of the installation's Teams app. Unlike
// every other target system its actions do not run in the sandbox: the
// credential is the installation's, and it can reach every tenant that
// installed the app, so it never leaves the control plane. The daemon sees
// RunsInControlPlane and hands the call over; Execute here is never the path.
type System struct{}

// Register puts the system into the target registry. Called from the
// binaries' bundled-plugin lists, so a `nopack` build leaves it out together
// with the Teams client it depends on.
func Register() {
	target.Register(target.Descriptor{
		Name:          Name,
		Label:         "Microsoft Teams (covey app)",
		Description:   "Chats and channels in Microsoft Teams through the installation's covey app: one app for every organisation and agent, no Azure registration per agent. Messages that name an agent reach it; agents reply and post into channels.",
		Kind:          "builtin",
		Category:      target.CategoryComms,
		Scopes:        []string{"read", "write"},
		System:        System{},
		NoCredentials: true,
		SetupDoc: `1. Settings → Microsoft Teams: "Connect Microsoft 365". Sign in as a
   Teams or Global Administrator of your Microsoft tenant.

2. Download the app package there and upload it in the Teams admin
   centre (or Teams → Apps → Upload a custom app), then add it to the
   teams whose channels agents should use.

3. Grant it in the agent's ACCESS.md:
   - system: teamsapp scope: read,write

4. In Teams: write to the covey app directly, or @mention it in a channel
   and start with the colleague's name ("@covey Vera, …").`,
	})
}

func (System) Name() string { return Name }

// RunsInControlPlane tells the daemon to hand every action to the control
// plane instead of executing it.
func (System) RunsInControlPlane() bool { return true }

func (System) ActionSubject(action string, _ json.RawMessage) string { return Name + ":" + action }

// ActionScope: listing channels reads, everything that writes into Teams
// needs write (#704).
func (System) ActionScope(action string) string {
	if action == "list_channels" {
		return "read"
	}
	return "write"
}

var errControlPlane = errors.New("teamsapp actions run in the control plane, not in the sandbox")

func (System) Execute(context.Context, string, json.RawMessage, target.Credential) (any, error) {
	return nil, errControlPlane
}

func (System) PromptDoc() string {
	return `Microsoft Teams through the covey app (system teamsapp). Teams shows
every message as coming from "covey"; your name stands in the first line of
the card, so do not sign your messages.

- reply {"conversation_id":"…","reply_to_activity_id":"…","text":"…"}
  Answer in a conversation you were addressed in. Both IDs are in the task
  that brought you the message. Without reply_to_activity_id the message
  goes into the conversation as a new one.
- list_channels {}
  The channels of the teams the app is installed in: team, channel name,
  channel_id.
- post {"channel":"<channel_id or Team/Channel>","text":"…"}
  Start a new thread in a channel. Replies in that thread come back to you.

Text is Markdown as Teams renders it (bold, lists, links).`
}
