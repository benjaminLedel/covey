// Package usersys is the agent-facing half of "Teams as a person" (#712),
// kept free of the control plane's dependencies so the daemon can know it.
package usersys

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// Name is the system's name in ACCESS.md: `- system: teamsuser scope: read,write`.
const Name = "teamsuser"

// System: every action runs in the control plane with the delegating
// person's token, which never reaches a sandbox.
type System struct{}

// Register puts the system into the target registry.
func Register() {
	target.Register(target.Descriptor{
		Name:          Name,
		Label:         "Microsoft Teams as a person",
		Description:   "A person lets an agent work in their own Teams through delegated access: read the chats and channels they are in, answer and post in their name, marked as sent by the agent. The agent sees exactly what the person sees.",
		Kind:          "builtin",
		Category:      target.CategoryComms,
		Scopes:        []string{"read", "write"},
		System:        System{},
		NoCredentials: true,
		SetupDoc: `1. Target systems → Teams as a person: choose the agent and
   "Connect my Teams". Sign in with your own Microsoft account.

2. Grant it in the agent's ACCESS.md:
   - system: teamsuser scope: read,write

3. Messages the agent sends appear under your name in Teams, with a line
   naming the agent. A guard rail on teamsuser:send_chat and
   teamsuser:post_channel can require your approval for each one.`,
	})
}

func (System) Name() string             { return Name }
func (System) RunsInControlPlane() bool { return true }

func (System) ActionSubject(action string, _ json.RawMessage) string { return Name + ":" + action }

func (System) ActionScope(action string) string {
	switch action {
	case "send_chat", "post_channel":
		return "write"
	}
	return "read"
}

func (System) Execute(context.Context, string, json.RawMessage, target.Credential) (any, error) {
	return nil, errors.New("teamsuser actions run in the control plane, not in the sandbox")
}

func (System) PromptDoc() string {
	return `Microsoft Teams as the person who connected you (system teamsuser).
You act with their access: you see exactly the chats, teams and channels
they see. What you send appears under their name, with a line saying you
sent it — write as their assistant, not as them.

- list_chats {}                          their chats: chat_id, topic, members
- read_chat {"chat_id":"…","limit":20}    the latest messages of a chat
- send_chat {"chat_id":"…","text":"…"}    send into a chat in their name
- list_channels {}                       teams and channels they belong to
- read_channel {"team_id":"…","channel_id":"…","limit":20}
- post_channel {"team_id":"…","channel_id":"…","text":"…","reply_to":"<message id, optional>"}

Read before you write, and send only what the task asks for.`
}
