package api

import (
	"context"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		lib, err := th.s.db.CreateLibrary(context.Background(), "Retry jobs", db.LibraryMovies, "en", []string{th.T.TempDir()})
		if err != nil {
			th.Fatal(err)
		}
		return []retryCase{
			{route: "POST /v1/jobs", body: JobStart{Kind: db.JobScan, Target: JobTarget{Library: lib.PublicID}},
				other: JobStart{Kind: db.JobScan, Target: JobTarget{Library: lib.PublicID}, Params: JSON(`{"allFiles":true}`)}},
		}
	})
}
