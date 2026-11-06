package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func statementID(name string) stmt.ID { return stmt.ID("read.browse." + name) }
