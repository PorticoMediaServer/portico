package read

import "testing"

func TestRSAudioAuthorSQLiteNormalization(t *testing.T) {
	f := audioRelationsFixture(t)
	// Independent API expression is deliberately kept in the oracle. These cases
	// distinguish SQLite trim/lower from Unicode case folding and TrimSpace.
	for _, title := range []string{"  ÉCOLE  ", "\tAUTHOR\t", "\u00a0AUTHOR\u00a0", " A\x00B ", "İSTANBUL", "   "} {
		f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=13001`, title)
		var want, got string
		if err := f.conn.QueryRowContext(t.Context(), `SELECT lower(trim(author.title)) FROM catalog_entities author WHERE author.id=13001`).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if err := f.conn.QueryRowContext(t.Context(), rsAudioAuthorKeySQL, int64(13001), int64(1)).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%q: got %q want %q", title, got, want)
		}
		var bytes int64
		if err := f.conn.QueryRowContext(t.Context(), rsAudioAuthorKeyOctetsSQL, int64(13001), int64(1)).Scan(&bytes); err != nil {
			t.Fatal(err)
		}
		if bytes != int64(len(got)) {
			t.Fatalf("%q: actual byte preflight=%d payload=%d", title, bytes, len(got))
		}
	}
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=13001`)
	rows, err := f.conn.QueryContext(t.Context(), rsAudioAuthorKeySQL, int64(13001), int64(1))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("retired author admitted")
	}
}
