package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/titledata/titledatatest"
)

func TestLocalSimilarUsesNarrowSignalsAndNeverPadsEnhancedData(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		statements := []string{
			`INSERT INTO users(id,public_id,username,role,created_at,updated_at) VALUES(1,'u','u','member',0,0)`,
			`INSERT INTO profiles(id,public_id,user_id,name,access_changed_at,created_at,updated_at) VALUES(1,'p1',1,'all',0,0,0),(2,'p2',1,'limited',0,0,0)`,
			`UPDATE profiles SET max_rating_age=13 WHERE id=2`,
			`INSERT INTO libraries(id,public_id,name,kind,created_at,updated_at) VALUES(1,'lib','Movies','movies',0,0)`,
			`INSERT INTO tags(id,public_id,kind,name,norm_name) VALUES(1,'g','genre','Drama','drama'),(2,'s','studio','Studio','studio'),
    (3,'n','network','Network','network'),(4,'b','label','Blocked','blocked')`,
			`INSERT INTO profile_restrictions(profile_id,tag_id) VALUES(2,4)`,
			`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<90)
    INSERT INTO items(id,public_id,library_id,kind,title,sort_title,rating_age,labels,added_at,updated_at)
    SELECT x,'i'||x,1,1,'Title '||x,'Title '||x,CASE WHEN x=11 THEN 17 WHEN x=12 THEN NULL ELSE 0 END,CASE WHEN x=10 THEN ',4,' ELSE NULL END,x,0 FROM n`,
			`INSERT INTO items(id,public_id,library_id,kind,title,sort_title,rating_age,added_at,updated_at) VALUES(100,'collection',1,13,'Collection','Collection',0,0,0)`,
			`INSERT INTO item_tags(item_id,tag_id,library_id,kind,rating_age) SELECT id,1,1,1,rating_age FROM items WHERE kind=1`,
			`INSERT INTO item_tags(item_id,tag_id,library_id,kind,rating_age) VALUES(1,2,1,1,0),(1,3,1,1,0),(2,2,1,1,0),(2,3,1,1,0)`,
			`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<9) INSERT INTO people(id,public_id,name,sort_name,updated_at) SELECT x,'p'||x,'Person '||x,'Person '||x,0 FROM n`,
			`INSERT INTO credits(item_id,position,person_id,role) VALUES(1,0,1,1),(1,1,2,1),(1,2,3,1),(1,3,4,1),(1,4,5,2),(1,5,6,3),
    (2,0,1,1),(3,0,5,2),(4,0,6,3),(6,0,7,1),(6,1,8,1),(6,2,9,1),(6,4,1,1),(9,0,4,1)`,
			`INSERT INTO credits(item_id,position,person_id,role) SELECT id,0,1,1 FROM items WHERE id BETWEEN 10 AND 14 OR id BETWEEN 20 AND 85`,
			`INSERT INTO item_relations(item_id,kind,related_item_id,position) VALUES(100,1,1,0),(100,1,5,1)`,
			`INSERT INTO user_item_state(profile_id,item_id,played,hidden,updated_at) VALUES(1,13,1,0,0),(1,14,0,1,0),(2,13,1,0,0),(2,14,0,1,0)`,
			`INSERT INTO item_external_ids(item_id,provider,external_id) VALUES(1,'tmdb','1001'),(4,'tmdb','1004')`,
		}
		for _, stmt := range statements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []int64{1, 2} {
		v := Viewer{UserID: 1, ProfileID: profile}
		got := []int64{}
		for cursor := ""; ; {
			page, err := d.MoreLikeThisPage(ctx, v, "i1", cursor, 7)
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range page.Items {
				if slices.Contains(got, it.ID) {
					t.Fatal("ranking repeated a title")
				}
				got = append(got, it.ID)
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
			if len(got) > 90 {
				t.Fatal("ranking did not end")
			}
		}
		if len(got) == 0 || got[0] != 2 {
			t.Fatalf("broad facts did not score narrow candidates: %v", got)
		}
		for _, id := range []int64{1, 6, 7, 8, 9, 13, 14} {
			if slices.Contains(got, id) {
				t.Fatalf("nonqualifying title %d", id)
			}
		}
		for _, id := range []int64{3, 4, 5, 20, 85} {
			if !slices.Contains(got, id) {
				t.Fatalf("lost narrow signal %d", id)
			}
		}
		if profile == 2 {
			for _, id := range []int64{10, 11, 12} {
				if slices.Contains(got, id) {
					t.Fatal("restricted title exposed")
				}
			}
		}
	}
	v := Viewer{UserID: 1, ProfileID: 1}
	if page, err := d.MoreLikeThisPage(ctx, v, "i7", "", 15); err != nil || len(page.Items) != 0 {
		t.Fatal("genre alone qualified a candidate", err)
	}
	for n, target := range []string{"1004", "999999"} {
		file := filepath.Join(t.TempDir(), fmt.Sprint("titles", n, ".sqlite"))
		titledatatest.Write(t, file, titledatatest.File{Works: []titledatatest.Work{
			{QID: 1, IDs: map[int]string{titledatatest.TMDBMovie: "1001"}, Similar: []int64{2}},
			{QID: 2, IDs: map[int]string{titledatatest.TMDBMovie: target}},
		}})
		if err := PrepareTitleData(ctx, file, "screen", "2026.45"); err != nil {
			t.Fatal(err)
		}
		if err := d.UseTitleData(file); err != nil {
			t.Fatal(err)
		}
		page, err := d.MoreLikeThisPage(ctx, v, "i1", "", 15)
		if err != nil {
			t.Fatal(err)
		}
		want := 1 - n
		if len(page.Items) != want || page.NextCursor != "" {
			t.Fatal("enhanced list received local padding")
		}
		if want == 1 && page.Items[0].ID != 4 {
			t.Fatal("enhanced ranking did not lead")
		}
	}
}
