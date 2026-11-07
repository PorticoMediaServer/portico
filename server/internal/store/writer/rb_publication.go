package writer

import (
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// rbPublication is private command-body state, not a worker callback or SQL
// capability. Phase2 closed command composition declares conservative fact
// scopes in governor.Request.Footprint BEFORE admission. Existing compositions
// do not enable PreparationProfile and therefore retain phase1a semantics.
type rbPublication struct {
	actual governor.PublicationFootprint
	err    error
}

// resolvePublication is called by a closed phase2 command after resolving its
// targets under current accepting-transaction authority. Calling it with a
// wider footprint poisons this turn even if a handler ignores the error: it
// must roll back, expand, then re-admit. A second call cannot erase that error.
// Unknown/no declared footprint is conservative GLOBAL publication.
func (t *transaction) resolvePublication(actual governor.PublicationFootprint) error {
	if t == nil || !t.begun || t.done {
		return ErrInvalid
	}
	c, ok := t.custody.(*governedTurn)
	if !ok {
		return ErrInvalid
	}
	if c.publication != nil {
		c.publication.err = governor.ErrPublicationFootprint
		return c.publication.err
	}
	state := &rbPublication{}
	c.publication = state
	if !governor.PublicationCovered(governor.PublicationFootprint(c.expected.Footprint), actual) {
		state.err = governor.ErrPublicationFootprint
	} else {
		state.actual = slices.Clone(actual)
	}
	return state.err
}

func (t *transaction) checkPublicationCommit() error {
	c, ok := t.custody.(*governedTurn)
	if !ok {
		return nil
	} // existing isolated frame/oracle infrastructure
	var actual governor.PublicationFootprint
	if c.publication != nil {
		if c.publication.err != nil {
			return c.publication.err
		}
		actual = c.publication.actual
	}
	// No declaration and no resolution is intentionally GLOBAL, not a resolved
	// empty effect. Admission already excludes every active preparation. Only
	// declared narrow footprints require an explicit resolved-effect marker.
	return c.a.CheckPublicationContext(t.ctx, c.grant, c.expected, actual)
}
