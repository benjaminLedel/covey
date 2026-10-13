package orchestrator

import (
	"testing"

	"covey/internal/chat"
	"covey/internal/daemon"
)

// TestMessageFileLimitsAgree: the daemon reads a covey/message's files under
// its limits, the control plane checks them again under its own (#586). Two
// numbers that drift apart would refuse in one place what the other let
// through — or let a frame past the WebSocket's read limit.
func TestMessageFileLimitsAgree(t *testing.T) {
	if daemon.MaxMessageFiles != chat.MaxAttachments || daemon.MaxMessageFileBytes != chat.MaxAttachmentBytes {
		t.Fatalf("daemon %d/%d, chat %d/%d", daemon.MaxMessageFiles, daemon.MaxMessageFileBytes,
			chat.MaxAttachments, chat.MaxAttachmentBytes)
	}
	// base64 of the limit, with room for the envelope, inside 16 MiB.
	if (chat.MaxAttachmentBytes+2)/3*4 > 15<<20 {
		t.Fatalf("the files of one message do not fit a 16 MiB frame")
	}
}
