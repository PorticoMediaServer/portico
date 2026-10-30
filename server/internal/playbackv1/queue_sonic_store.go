package playbackv1

import (
	"context"
	"database/sql"
	"encoding/hex"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func validSonicWindowKey(key sonicWindowKey) bool {
	return key.queue != "" && key.seed >= 0 && key.lap >= 0 && key.window >= 0
}

// The caller owns current queue/profile admission and its layout fence. These
// helpers never grant authority, start a snapshot, or choose another model.
func readSonicWindowTx(ctx context.Context, tx *sql.Tx, key sonicWindowKey, ordinary []int64) (sonicWindowCapture, error) {
	out := sonicWindowCapture{key: key}
	if tx == nil || !validSonicWindowKey(key) {
		return out, errSonicWindow
	}
	var id, version, digest string
	var count int
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT model_id,model_version,model_sha256,count,ordinals FROM queue_v1_sonic_windows WHERE queue_id=? AND seed=? AND lap=? AND window_index=? AND contents=?`, key.queue, key.seed, key.lap, key.window, key.contents[:]).Scan(&id, &version, &digest, &count, &raw)
	if err != nil {
		return out, err
	}
	if count != len(ordinary) {
		return out, errSonicWindow
	}
	if id != "" || version != "" || digest != "" {
		sha, err := hex.DecodeString(digest)
		if id == "" || len(id) > 128 || version == "" || len(version) > 128 || err != nil || len(sha) != 32 {
			return out, errSonicWindow
		}
		// An older captured model remains valid after the installed model changes.
		out.model = &sonic.Identity{ID: id, Version: version, SHA256: digest}
	}
	out.ordinals, err = sonicWindowOrdinals(raw, ordinary)
	if err == nil && out.model == nil && !slices.Equal(out.ordinals, ordinary) {
		return out, errSonicWindow
	}
	return out, err
}

func captureSonicWindowTx(ctx context.Context, tx *sql.Tx, key sonicWindowKey, ordinary, proposed []int64, model *sonic.Identity) (sonicWindowCapture, error) {
	if tx == nil || !validSonicWindowKey(key) {
		return sonicWindowCapture{}, errSonicWindow
	}
	raw, err := sonicWindowBytes(ordinary, proposed)
	if err != nil {
		return sonicWindowCapture{}, err
	}
	id, version, digest := "", "", ""
	if model == nil && !slices.Equal(ordinary, proposed) {
		return sonicWindowCapture{}, errSonicWindow
	}
	if model != nil {
		if model.ID != sonic.ModelID || model.Version != sonic.ModelVersion || model.SHA256 != sonic.ModelSHA256 {
			return sonicWindowCapture{}, errSonicWindow
		}
		id, version, digest = model.ID, model.Version, model.SHA256
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO queue_v1_sonic_windows(queue_id,seed,lap,window_index,contents,model_id,model_version,model_sha256,count,ordinals) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(queue_id,seed,lap,window_index,contents) DO NOTHING`, key.queue, key.seed, key.lap, key.window, key.contents[:], id, version, digest, len(ordinary), raw)
	if err != nil {
		return sonicWindowCapture{}, err
	}
	// A concurrent capture, old model, or prior ordinary fallback always wins.
	return readSonicWindowTx(ctx, tx, key, ordinary)
}
