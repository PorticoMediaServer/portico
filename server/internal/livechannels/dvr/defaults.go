package dvr

import (
	"context"
	"database/sql"
)

// RecordingLayout is the normalized template policy observed in a scheduler
// snapshot. The driver owns the confined recording root; this is neither a
// filesystem location nor permission to publish there.
type RecordingLayout struct {
	Revision      int64
	ShowTemplate  string
	MovieTemplate string
}

// ReadRecordingLayoutTx reads the same defaults revision as the owner writer.
// A missing policy cannot silently select the private layout: the scheduler
// must retain the read error until the actual policy can be observed.
func ReadRecordingLayoutTx(ctx context.Context, tx *sql.Tx) (RecordingLayout, error) {
	if tx == nil {
		return RecordingLayout{}, ErrInvalid
	}
	var out RecordingLayout
	err := tx.QueryRowContext(ctx, `SELECT revision,show_folder_template,movie_folder_template FROM dvr_defaults WHERE singleton=1`).Scan(&out.Revision, &out.ShowTemplate, &out.MovieTemplate)
	if err != nil {
		return RecordingLayout{}, err
	}
	if out.Revision < 1 {
		return RecordingLayout{}, ErrInvalid
	}
	return out, nil
}

func DefaultOptionsTx(ctx context.Context, tx *sql.Tx) (Options, error) {
	var o Options
	err := tx.QueryRowContext(ctx, `SELECT before_seconds*1000,after_seconds*1000,retention_days,episode_limit FROM dvr_defaults WHERE singleton=1`).Scan(&o.BeforeMs, &o.AfterMs, &o.RetentionDays, &o.EpisodeLimit)
	return o, err
}
