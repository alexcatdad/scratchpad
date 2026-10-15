package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

func TestSafeStartupMessagesClassifyWithoutDetails(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{localcapture.ErrKeyUnavailableSentinel, "encrypted capture queue"},
		{localcapture.ErrConflictSentinel, "another instance may be active"},
		{localcapture.ErrStorageFailedSentinel, "private capture storage"},
		{errors.New("synthetic private path and key material"), "start or stopped safely"},
	} {
		message := safeStartupMessage(test.err)
		if !strings.Contains(message, test.want) || strings.Contains(message, "synthetic") || strings.Contains(message, "private path") {
			t.Fatalf("unsafe or unhelpful startup message %q", message)
		}
	}
}
