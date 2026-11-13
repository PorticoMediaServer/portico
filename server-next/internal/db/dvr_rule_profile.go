package db

import "context"

// DvrRuleViewer resolves the profile an owner chose, without admitting a disabled account.
func (d *DB) DvrRuleViewer(ctx context.Context, profile string) (Viewer, error) {
	var out Viewer
	err := d.read.QueryRowContext(ctx, `SELECT p.user_id, p.id FROM profiles p JOIN users u ON u.id = p.user_id
 WHERE p.public_id = ? AND u.state = 'active' AND u.disabled_at IS NULL`, profile).Scan(&out.UserID, &out.ProfileID)
	return out, notFound(err)
}
