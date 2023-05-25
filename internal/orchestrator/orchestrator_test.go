package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindSubjectOrigin(t *testing.T) {
	wellFormed := "com.hollow.sh.serverservice.events.server.create"
	bogus := "bogus.garbage"

	origin := findSubjectOrigin(wellFormed)
	require.Equal(t, serverServiceOrigin, origin)

	origin = findSubjectOrigin(bogus)
	require.Equal(t, defaultOrigin, origin)
}
