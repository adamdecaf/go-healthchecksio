package healthchecksio_test

// GO_HEALTHCHECKSIO_API_KEY
// GO_HEALTHCHECKSIO_PING_KEY

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adamdecaf/go-healthchecksio/pkg/healthchecksio"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func setupTestClient(tb testing.TB) healthchecksio.Client {
	tb.Helper()

	apiKey := os.Getenv("GO_HEALTHCHECKSIO_API_KEY")

	if apiKey == "" {
		tb.Skip("Skipping integration tests: GO_HEALTHCHECKSIO_API_KEY must be set")
	}

	return healthchecksio.NewClient(apiKey)
}

func randomSlug(tb testing.TB) string {
	return strings.ToLower(tb.Name()) + "-" + uuid.NewString()[:8]
}

func TestCheckLifecycle(t *testing.T) {
	client := setupTestClient(t)

	name := "integration-test-check-" + uuid.New().String()[:8]
	createReq := &healthchecksio.CreateCheck{
		Name:  name,
		Slug:  randomSlug(t),
		Tags:  "integration-test go-client",
		Grace: 60,
	}

	created, err := client.CreateCheck(createReq)
	require.NoError(t, err)
	require.NotEmpty(t, created.UUID)
	require.Equal(t, createReq.Name, created.Name)
	require.Equal(t, createReq.Slug, created.Slug)

	// Defer cleanup (always runs, even on panic/failure)
	t.Cleanup(func() {
		_, err := client.DeleteCheck(created.UUID)
		if err != nil {
			t.Logf("Warning: Failed to delete check %s during cleanup: %v", created.UUID, err)
		}
	})

	// Get the check by UUID
	gotByUUID, err := client.GetCheck(created.UUID)
	require.NoError(t, err)
	require.Equal(t, created.UUID, gotByUUID.UUID)
	require.Equal(t, created.Name, gotByUUID.Name)

	// List checks and verify ours is there
	listResp, err := client.GetChecks(healthchecksio.GetChecks{
		Tags: "integration-test",
	})
	require.NoError(t, err)

	var found bool
	for _, ch := range listResp.Checks {
		if ch.UUID == created.UUID {
			found = true
			break
		}
	}
	require.True(t, found, "created check not found in list with tag filter")

	// Update the check
	updateReq := &healthchecksio.UpdateCheck{
		Name:    "Updated Name",
		Timeout: 60,
		Grace:   3600,
		Tags:    "integration-test updated",
	}
	updated, err := client.UpdateCheck(created.UUID, updateReq)
	require.NoError(t, err)
	require.Equal(t, "Updated Name", updated.Name)
	require.Equal(t, 3600, updated.Grace)

	// Pause the check
	paused, err := client.PauseCheck(created.UUID)
	require.NoError(t, err)
	require.NotNil(t, paused)

	// Resume the check
	resumed, err := client.ResumeCheck(created.UUID)
	require.NoError(t, err)
	require.NotNil(t, resumed)

	// Send a ping (success)
	err = client.Ping(created.PingURL, "")
	require.NoError(t, err)

	// Give HC a moment to process the ping
	time.Sleep(2 * time.Second)

	// Verify ping appeared
	pings, err := client.GetPings(created.UUID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(pings.Pings), 1)

	require.Equal(t, "success", pings.Pings[0].Type)
	require.Equal(t, 1, pings.Pings[0].N)

	// Send a failure ping
	err = client.Ping(created.PingURL, "example body", healthchecksio.WithFail())
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	// Verify failure ping
	pings, err = client.GetPings(created.UUID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(pings.Pings), 2)

	// Get ping body (most recent)
	body, err := client.GetPingBody(created.UUID, 2)
	require.NoError(t, err)
	require.Equal(t, "example body", body)

	// Check flips
	flips, err := client.GetFlips(created.UUID, healthchecksio.GetFlipsRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, flips.Flips)
}

func TestCreateWithMinimalFields(t *testing.T) {
	client := setupTestClient(t)

	created, err := client.CreateCheck(&healthchecksio.CreateCheck{
		Name: "minimal-check-" + uuid.New().String()[:8],
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.UUID)

	t.Cleanup(func() {
		client.DeleteCheck(created.UUID)
	})
}
