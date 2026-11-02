// Scratch (server load review): time a fresh connection's schema load and a
// trivial read with the server's own pure-Go SQLite driver.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"time"

	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func main() {
	for i := 0; i < 5; i++ {
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		db, _ := sql.Open("sqlite", "file:"+os.Args[1]+"?mode=ro")
		db.SetMaxOpenConns(1)
		start := time.Now()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM libraries`).Scan(&n); err != nil {
			panic(err)
		}
		first := time.Since(start)
		start = time.Now()
		db.QueryRow(`SELECT count(*) FROM libraries`).Scan(&n)
		runtime.ReadMemStats(&m1)
		fmt.Printf("first query on new connection %v, second %v, heap+%d KB\n", first, time.Since(start), (m1.HeapAlloc-m0.HeapAlloc)/1024)
		db.Close()
	}
}
