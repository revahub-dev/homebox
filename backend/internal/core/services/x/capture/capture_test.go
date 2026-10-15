package capture

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBucketOf(t *testing.T) {
	assert.Equal(t, "ready", bucketOf(0.95, false))
	assert.Equal(t, "ready", bucketOf(ReadyConfidence, false))
	assert.Equal(t, "review", bucketOf(0.79, false))
	assert.Equal(t, "review", bucketOf(0.95, true), "low-confidence flag demotes")
	assert.Equal(t, "review", bucketOf(ReviewConfidence, false))
	assert.Equal(t, "maybe", bucketOf(0.49, false))
}

func TestHasStrongDup(t *testing.T) {
	assert.False(t, hasStrongDup(nil))
	assert.False(t, hasStrongDup([]DupMatch{{Confidence: "similar"}}))
	assert.True(t, hasStrongDup([]DupMatch{{Confidence: "similar"}, {Confidence: "likely"}}))
	assert.True(t, hasStrongDup([]DupMatch{{Confidence: "exact"}}))
}

func TestFriendlyError(t *testing.T) {
	assert.Contains(t, friendlyError(assertErr("dial tcp 127.0.0.1:1: connect: connection refused")), "HBOX_AI_BASE_URL")
	assert.Contains(t, friendlyError(assertErr("Client.Timeout exceeded while awaiting headers")), "HBOX_AI_TIMEOUT_SEC")
	assert.Contains(t, friendlyError(errBadImage), "could not be read")
}

type strErr string

func (e strErr) Error() string { return string(e) }

func assertErr(s string) error { return strErr(s) }
