package db

import "context"

// ChannelMoodLabels gives the block-preset picker the title data's human names, never its keys.
func (d *DB) ChannelMoodLabels(ctx context.Context) (map[string]string, error) {
	td := d.titleData.Load()
	if td == nil {
		return nil, nil
	}
	return td.rowLabels(ctx)
}
