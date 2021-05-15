package macaroons

import (
	"context"

	"github.com/pkt-cash/pktd/btcutil/er"
)

var (
	// RootKeyIDContextKey is the key to get rootKeyID from context.
	RootKeyIDContextKey = contextKey{"rootkeyid"}

	// ErrContextRootKeyID is used when the supplied context doesn't have
	// a root key ID.
	ErrContextRootKeyID = Err.CodeWithDetail("ErrContextRootKeyID", "failed to read root key ID "+
		"from context")
)

// contextKey is the type we use to identify values in the context.
type contextKey struct {
	Name string
}

// ContextWithRootKeyID passes the root key ID value to context.
func ContextWithRootKeyID(ctx context.Context,
	value interface{}) context.Context {

	return context.WithValue(ctx, RootKeyIDContextKey, value)
}

// RootKeyIDFromContext retrieves the root key ID from context using the key
// RootKeyIDContextKey.
func RootKeyIDFromContext(ctx context.Context) ([]byte, er.R) {
	id, ok := ctx.Value(RootKeyIDContextKey).([]byte)
	if !ok {
		return nil, ErrContextRootKeyID.Default()
	}

	// Check that the id is not empty.
	if len(id) == 0 {
		return nil, ErrMissingRootKeyID.Default()
	}

	return id, nil
}
