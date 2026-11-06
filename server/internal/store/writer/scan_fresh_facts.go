package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type inventoryFreshFacts struct {
	discovered, processed, membership, ageTotal                  int64
	ageExists                                                    bool
	data, order, metadata, ageInput, agePublished, agePending    int64
	newDiscovered, newProcessed, newMembership, newAgeTotal      int64
	newData, newOrder, newMetadata, newAgeInput, newAgePublished int64
}

func inventoryRunArgs(c *inventoryAcceptCommand) []any {
	r := c.request
	return []any{r.scope.runID, r.source.id, r.source.generation, r.source.incarnation, r.source.rootIdentity, r.run.policyRevision,
		r.run.phase, r.run.scanMode, r.run.leaseOwner, r.run.leaseGeneration, r.run.leaseExpiresMS, r.run.revision,
		c.identity.Incarnation[:], r.run.lastSequence, r.run.acknowledgedSequence}
}

func inventorySourceArgs(c *inventoryAcceptCommand) []any {
	r := c.request
	var follow int64
	if r.source.followSymlinks {
		follow = 1
	}
	return []any{r.source.id, r.scope.libraryKey, r.source.generation, r.source.incarnation, r.source.rootIdentity, string(r.source.resolvedRoot), follow}
}

func inventoryDirectoryArgs(c *inventoryAcceptCommand) []any {
	r := c.request
	return []any{r.scope.runID, r.directory.path, r.directory.identity, r.directory.revision, r.directory.expectedCursor, r.directory.pages, r.directory.skipped}
}

func inventoryNext(value int64) (int64, error) {
	if value < 0 || value == math.MaxInt64 {
		return 0, ErrConflict
	}
	return value + 1, nil
}

func readInventoryFreshFacts(ctx context.Context, turn *commandTurn, c *inventoryAcceptCommand) (out inventoryFreshFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	r := c.request
	row, err := turn.queryRow(ctx, turn.steps[scanFreshRun].statementStep, inventoryRunArgs(c)...)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.discovered); err != nil {
		return out, inventoryFreshReadError(err)
	}
	row, err = turn.queryRow(ctx, turn.steps[scanFreshSource].statementStep, inventorySourceArgs(c)...)
	if err != nil {
		return out, err
	}
	var source string
	if err = row.Scan(&source); err != nil {
		return out, inventoryFreshReadError(err)
	}
	if source != r.source.id {
		return out, ErrConflict
	}
	row, err = turn.queryRow(ctx, turn.steps[scanFreshLibrary].statementStep, r.scope.libraryKey, r.files[0].metadataLanguage)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.data, &out.order, &out.metadata, &out.ageInput, &out.agePublished, &out.agePending); err != nil {
		return out, inventoryFreshReadError(err)
	}
	if out.data <= 0 || out.order <= 0 || out.metadata <= 0 || out.agePublished <= 0 || out.agePublished > out.ageInput || out.agePending < 0 {
		return out, ErrConflict
	}
	row, err = turn.queryRow(ctx, turn.steps[scanFreshPolicy].statementStep, r.scope.libraryKey, r.run.policyRevision)
	if err != nil {
		return out, err
	}
	var policy int64
	if err = row.Scan(&policy); err != nil {
		return out, inventoryFreshReadError(err)
	}
	if policy != r.run.policyRevision {
		return out, ErrConflict
	}
	row, err = turn.queryRow(ctx, turn.steps[scanFreshJob].statementStep, r.scope.runID, r.scope.libraryKey)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.processed); err != nil {
		return out, inventoryFreshReadError(err)
	}
	row, err = turn.queryRow(ctx, turn.steps[scanFreshDirectory].statementStep, inventoryDirectoryArgs(c)...)
	if err != nil {
		return out, err
	}
	var pages, skipped int64
	if err = row.Scan(&pages, &skipped); err != nil {
		return out, inventoryFreshReadError(err)
	}
	if pages != r.directory.pages || skipped != r.directory.skipped {
		return out, ErrConflict
	}
	// NEW-only. Never overwrite/reimport a retained identity, asset or live path.
	for _, point := range []struct {
		id   stmt.ID
		args []any
	}{
		{scanInventoryIdentity, []any{r.scope.libraryID, r.files[0].sourceKey}},
		{scanInventoryAsset, []any{r.scope.libraryID, r.files[0].assetPath}},
		{scanFreshPath, []any{r.source.id, r.files[0].relativePath}},
	} {
		rows, e := turn.query(ctx, turn.steps[point.id].statementStep, point.args...)
		if e != nil {
			return out, e
		}
		found := rows.Next()
		e = errors.Join(rows.Err(), rows.Close())
		if e != nil {
			return out, e
		}
		if found {
			return out, ErrConflict
		}
	}
	row, err = turn.queryRow(ctx, turn.steps[scanFreshMembership].statementStep)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.membership); err != nil {
		return out, inventoryFreshReadError(err)
	}
	row, err = turn.queryRow(ctx, turn.steps[scanFreshAge].statementStep, r.scope.libraryID, scanNewMovieKind, int64(0), int64(-1))
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.ageTotal)
	if errors.Is(err, sql.ErrNoRows) {
		err, out.ageTotal = nil, 0
	} else if err != nil {
		return out, err
	} else {
		out.ageExists = true
	}
	for _, p := range []struct {
		old  int64
		next *int64
	}{
		{out.discovered, &out.newDiscovered}, {out.processed, &out.newProcessed}, {out.membership, &out.newMembership}, {out.ageTotal, &out.newAgeTotal},
		{out.data, &out.newData}, {out.order, &out.newOrder}, {out.metadata, &out.newMetadata}, {out.ageInput, &out.newAgeInput},
	} {
		*p.next, err = inventoryNext(p.old)
		if err != nil {
			return out, err
		}
	}
	out.newAgePublished = out.agePublished
	if out.ageInput == out.agePublished && out.agePending == 0 {
		out.newAgePublished = out.newAgeInput
	}
	if _, err = inventoryNext(r.run.revision); err != nil {
		return out, err
	}
	if _, err = inventoryNext(r.directory.pages); err != nil {
		return out, err
	}
	return out, nil
}

func inventoryFreshReadError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}

// Domain-only scalar CAS. No SQL strings, transaction or registration escape.
func execInventoryFreshOne(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) error {
	result, err := turn.exec(ctx, turn.steps[id].statementStep, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return turn.checkLifetimes(ctx)
}
