package resumestore_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/internal/adapters/resumestore"
	core "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

func newStore(t *testing.T) (*resumestore.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "_created_records.csv")
	s, err := resumestore.New(path)
	require.NoError(t, err)
	return s, path
}

func TestStore_CommitThenLoaded_CreatesParentDir(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	require.False(t, s.Loaded("k1"))
	require.NoError(t, s.Commit("k1", "rec-1"))
	require.True(t, s.Loaded("k1"))
}

func TestStore_CreatedIDs_RoundTripsInCommitOrder(t *testing.T) {
	t.Parallel()

	s, path := newStore(t)
	require.NoError(t, s.Commit("k1", "rec-1"))
	require.NoError(t, s.Commit("k2", "rec-2"))

	reopened, err := resumestore.New(path)
	require.NoError(t, err)
	require.True(t, reopened.Loaded("k1"))
	require.True(t, reopened.Loaded("k2"))

	ids, err := reopened.CreatedIDs()
	require.NoError(t, err)
	require.Equal(t, []string{"rec-1", "rec-2"}, ids)
}

func TestStore_InFlight_MarkClearRoundTrip(t *testing.T) {
	t.Parallel()

	s, path := newStore(t)
	b1 := core.Batch{ID: "acc:k1", AccountID: "acc", RowKeys: []string{"k1", "k2"}, MinDate: "a", MaxDate: "b"}
	b2 := core.Batch{ID: "acc:k9", AccountID: "acc", RowKeys: []string{"k9"}}
	require.NoError(t, s.MarkInFlight(b1))
	require.NoError(t, s.MarkInFlight(b2))

	got, err := s.InFlight()
	require.NoError(t, err)
	require.Equal(t, []core.Batch{b1, b2}, got)

	require.NoError(t, s.ClearInFlight("acc:k1"))
	got, err = s.InFlight()
	require.NoError(t, err)
	require.Equal(t, []core.Batch{b2}, got)

	// a fresh Store over the same path sees the same journal state
	reopened, err := resumestore.New(path)
	require.NoError(t, err)
	got, err = reopened.InFlight()
	require.NoError(t, err)
	require.Equal(t, []core.Batch{b2}, got)

	require.NoError(t, s.ClearInFlight("acc:k9"))
	got, err = s.InFlight()
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestStore_New_MissingFiles_NotAnError(t *testing.T) {
	t.Parallel()

	s, err := resumestore.New(filepath.Join(t.TempDir(), "absent.csv"))
	require.NoError(t, err)
	require.False(t, s.Loaded("anything"))

	ids, err := s.CreatedIDs()
	require.NoError(t, err)
	require.Empty(t, ids)

	batches, err := s.InFlight()
	require.NoError(t, err)
	require.Empty(t, batches)
}
