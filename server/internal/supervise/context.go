package supervise

import "context"

type operationKey struct{}

// Operation returns the server-owned name of the innermost supervised loop.
// Descendant contexts inherit it across batches and restarts. It conveys no
// authority and never comes from request input.
func Operation(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	name, _ := ctx.Value(operationKey{}).(string)
	return name
}
