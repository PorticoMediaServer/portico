package reading

import (
	"context"
	"time"
)

// AfterFunc wakes blocked IO, while Err checks the retained session directly.
// Cancellation propagation alone may run after a caller has started its next
// extent. A closed reader session must never win that race by returning bytes.
type sessionRequestContext struct {
	context.Context
	owner context.Context
}

func (c *sessionRequestContext) Err() error {
	if err := c.owner.Err(); err != nil {
		return err
	}
	return c.Context.Err()
}

func (c *sessionRequestContext) Deadline() (time.Time, bool) {
	a, aOK := c.Context.Deadline()
	b, bOK := c.owner.Deadline()
	if bOK && (!aOK || b.Before(a)) {
		return b, true
	}
	return a, aOK
}
