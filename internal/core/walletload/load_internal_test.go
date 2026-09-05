package walletload

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdoptExisting(t *testing.T) {
	t.Parallel()

	cats := []Category{{ID: "c1", Name: "Foo"}}
	list := func(context.Context) ([]Category, error) { return cats, nil }
	nameID := func(c Category) (string, string) { return c.Name, c.ID }
	ctx := context.Background()

	t.Run("not an ErrAlreadyExists -> not adopted", func(t *testing.T) {
		t.Parallel()
		_, ok := adoptExisting(ctx, errors.New("boom"), "Foo", list, nameID)
		require.False(t, ok)
	})

	t.Run("disclosed id is used verbatim", func(t *testing.T) {
		t.Parallel()
		id, ok := adoptExisting(ctx, &ErrAlreadyExists{ID: "x"}, "Foo", list, nameID)
		require.True(t, ok)
		require.Equal(t, "x", id)
	})

	t.Run("no id -> re-read catalogue and match by name (case-insensitive)", func(t *testing.T) {
		t.Parallel()
		id, ok := adoptExisting(ctx, &ErrAlreadyExists{}, "foo", list, nameID)
		require.True(t, ok)
		require.Equal(t, "c1", id)
	})

	t.Run("no id and no catalogue match -> not adopted", func(t *testing.T) {
		t.Parallel()
		_, ok := adoptExisting(ctx, &ErrAlreadyExists{}, "Missing", list, nameID)
		require.False(t, ok)
	})

	t.Run("no id and catalogue re-read fails -> not adopted", func(t *testing.T) {
		t.Parallel()
		failing := func(context.Context) ([]Category, error) { return nil, errors.New("network") }
		_, ok := adoptExisting(ctx, &ErrAlreadyExists{}, "Foo", failing, nameID)
		require.False(t, ok)
	})
}
