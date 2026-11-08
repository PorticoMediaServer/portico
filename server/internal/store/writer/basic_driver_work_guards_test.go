package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"testing"
)

func TestBasicDriverWorkIndependentGrantGuards(t *testing.T) {
	for _, guard := range []string{"writer", "class", "reverse_mode"} {
		t.Run(guard, func(t *testing.T) {
			d, p, q := basicDriverWorkFixture(t, BasicStage)
			r := d.writer.effective
			// Replace the test holding with a genuinely admitted matching grant. Merely
			// altering expected would fail Authority.Check and mask the work guard.
			if err := r.authority.Retired(p.grant); err != nil {
				t.Fatal(err)
			}
			purpose := BasicStage
			switch guard {
			case "writer":
				p.expected.Writer = true
				p.expected.Need[governor.WALBytes] = 65536
			case "class":
				p.expected.Class = governor.Foreground
			case "reverse_mode":
				purpose = InventoryCreate
				p.expected.Key.Domain = uint16(purpose)
				p.expected.Footprint[0].Domain = uint16(purpose)
			}
			decision := r.governor.Try(p.expected)
			if decision.Grant == nil {
				t.Fatal("test guard funding", decision.Refusal)
			}
			p.grant = decision.Grant
			if err := p.current(); err != nil {
				t.Fatal("guard was masked by grant mismatch", err)
			}
			work, err := d.prepareWork(t.Context(), purpose, q, 5, p)
			if !errors.Is(err, ErrInvalid) || work != nil || d.work != nil || p.retired {
				t.Fatal("work guard bypassed", work, err)
			}
		})
	}
}
