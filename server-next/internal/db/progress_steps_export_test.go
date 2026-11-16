package db

import "context"

// SaveProgressBetweenSteps routes the real SaveProgress transactions through a
// test-owned queue, pausing after its first commit. The live DB's writer and SQL
// remain unchanged; the test can make a user write before the next step is sent.
func SaveProgressBetweenSteps(ctx context.Context, d *DB, batch []Progress, holding chan<- struct{}, release <-chan struct{}) error {
	proxy := &DB{queues: d.queues, closed: d.closed}
	proxy.queues[Background] = make(chan writeRequest)
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		first := true
		for {
			select {
			case <-stop:
				return
			case req := <-proxy.queues[Background]:
				if first {
					first = false
					caller := req.done
					req.done = make(chan error, 1)
					d.queues[Background] <- req
					err := <-req.done
					close(holding)
					<-release
					caller <- err
				} else {
					d.queues[Background] <- req
				}
			}
		}
	}()
	_, err := proxy.SaveProgress(ctx, Background, batch)
	close(stop)
	<-stopped
	return err
}
