package readfixture

import (
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func LiveConfig(now func() time.Time) governor.Config {
	gc := governor.Config{Now: now, MaxGrantsPerClass: 16, Foreground: &governor.ForegroundProfile{
		AssignedBytes: 1_600_000_000, EffectiveBytes: 1_600_000_000, NonBrowseBytes: 1140 << 20,
		MinimumLive: 4, MaxWaiters: 1, BookkeepingBytes: 4096, MaxWait: 50 * time.Millisecond,
	}}
	gc.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: (80 << 20) + 5*4096, governor.QueueBytes: 5 * 4096}
	return gc
}

// C019 catalog_entities has 129 columns. SQLite reparses the full CREATE on
// schema-cookie changes (including ANALYZE), even for a narrow SELECT. Keep
// this fixture reader bound large enough for that adopted table.
func ReaderConfig() sqliteconn.Config {
	return sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 129}}
}
func ReadRequest() governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 73, ID: "foreground-test"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
}
func Requests(id string, entry time.Time) (governor.ForegroundRequest, governor.Request, governor.Request) {
	q := governor.ForegroundRequest{Key: governor.JobKey{Domain: 73, ID: id}, Owner: 1, ReadyDeadline: entry.Add(time.Second), ProtectedDeadline: entry.Add(3 * time.Second)}
	request := ReadRequest()
	request.Key = q.Key
	request.Deadline = q.ProtectedDeadline
	output := governor.Request{Key: governor.JobKey{Domain: 74, ID: id}, Owner: q.Owner, Class: governor.Foreground, Need: governor.Amounts{governor.MemoryBytes: 8 << 20}, Deadline: q.ProtectedDeadline}
	return q, request, output
}
