package writer

import "context"

// Reserved dedicated mode, not an N1 observation or history reservation. The
// W-owned constructor remains closed until its Basic preparation is integrated.
const scanBasicPreparation scanPreparationMode = 3

func basicPreparationPurpose(purpose CommandID) bool {
	return purpose == BasicStage || purpose == BasicSeal || purpose == BasicAccept
}

func validateBasicWork(r *effectiveRuntime, w *Writer, scan *inventoryInvocation) error {
	if scan == nil || scan.basic == nil || scan.disclosure == nil || scan.disclosure.scope == nil {
		return ErrInvalid
	}
	in, driver := scan.basic, scan.disclosure.scope.driver
	if driver == nil {
		if in.work != nil {
			return ErrInvalid
		}
		return nil // only private component fixtures lack an issued driver
	}
	if driver.self != driver || driver.writer != w || w == nil || w.effective != r {
		return ErrDisclosureDenied
	}
	work := in.work
	kind, err := in.validate()
	if err != nil {
		return err
	}
	if work == nil || work.driver != driver || driver.work != work || work.purpose != kind || work.original != scan.request || work.generation < 0 ||
		work.preparation == nil || driver.preparation != work.preparation {
		return &Refusal{Code: RefusalUnqualified}
	}
	p := work.preparation
	if p.self != p || p.mode != scanBasicPreparation || p.opening != driver.opening || p.runtime != r || p.expected.Writer ||
		p.expected.Key.Domain != uint16(kind) || p.expected.Key.ID != scan.request.RunID ||
		len(p.expected.Footprint) != 1 || p.expected.Footprint[0].Library != scan.request.LibraryID || p.expected.Footprint[0].Domain != uint16(kind) {
		return ErrInvalid
	}
	return p.current()
}

func basicWorkCurrent(scan *inventoryInvocation) error {
	if scan != nil && scan.basic != nil && scan.basic.work != nil {
		return scan.basic.work.preparation.current()
	}
	return nil
}

// Receipt resolution happens first. Only a genuinely fresh effect checks the
// submitted physical generation against the original native fresh lease vector.
func checkBasicFreshWork(in *basicInvocation) error {
	if in == nil {
		return ErrInvalid
	}
	if in.work == nil {
		return nil
	}
	generation := int64(0)
	if in.stage != nil {
		generation = in.stage.request.original.run.leaseGeneration
	}
	if in.seal != nil {
		generation = in.seal.request.original.run.leaseGeneration
	}
	if generation <= 0 || generation != in.work.generation {
		return ErrConflict
	}
	return in.work.preparation.current()
}

// Same outer lifetime check also runs before physical terminal publication.
func checkBasicWorkLifetime(ctx context.Context, scan *inventoryInvocation) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return basicWorkCurrent(scan)
}
