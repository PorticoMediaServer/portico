package streaming

import (
	"fmt"
	"time"
)

func operation(n int) string {
	return fmt.Sprintf("%013d-00000000-0000-0000-0000-%012d", time.Now().UnixMilli(), n)
}
