package db

import (
	"container/list"
	"context"
	"database/sql/driver"
)

// Read connections (the main database's read pool and guide.db's) keep the statements they
// run prepared. SQLite parses and plans a statement each time it is given its text, and for
// Home that cost about as much as running it: Home sends around 120 statements, the same
// texts request after request, and in a crowd of 200 people SQL preparation was 16% of the
// server's processor (load run 4b, 18 Nov 2026). So each read connection keeps the statements
// it has run, by their text, and runs one again without parsing it. database/sql hands a
// connection to one request at a time, so what a connection keeps needs no lock.

// keptStatements is how many statements each read connection keeps, the least recently run
// leaving first. A text is the same for every profile limited the same way, and Home runs about
// 40 distinct ones for a profile that sees everything and 50 for a limited one; texts that name
// their ids are seldom run again. 400 leaves room for the pages of several kinds of profile at
// once, at up to about 10 MB a connection (a limited profile's average 25 KB at 400,000 titles).
const keptStatements = 400

// keepStatements opens read connections.
type keepStatements struct{ driver.Connector }

func (k keepStatements) Connect(ctx context.Context) (driver.Conn, error) {
	c, err := k.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &keptConn{readConn: c.(readConn), byText: map[string]*list.Element{}}, nil
}

// readConn is what database/sql uses of a SQLite connection.
type readConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.QueryerContext
	driver.ExecerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

// keptConn is a read connection with its kept statements, the most recently run first.
type keptConn struct {
	readConn
	byText map[string]*list.Element
	used   list.List
}

type keptStatement struct {
	text string
	stmt driver.Stmt
	// reading is set while the statement's rows are open, and dropped when it left the
	// connection's statements then: it is closed when its rows close.
	reading, dropped bool
}

func (c *keptConn) QueryContext(ctx context.Context, text string, args []driver.NamedValue) (driver.Rows, error) {
	var s *keptStatement
	if e := c.byText[text]; e != nil {
		c.used.MoveToFront(e)
		s = e.Value.(*keptStatement)
	} else {
		stmt, err := c.readConn.PrepareContext(ctx, text)
		if err != nil {
			return nil, err
		}
		s = &keptStatement{text: text, stmt: stmt}
		c.byText[text] = c.used.PushFront(s)
		if c.used.Len() > keptStatements {
			c.drop(c.used.Back())
		}
	}
	if s.reading {
		// The same text again while the first rows are still being read: parsed once more.
		return c.readConn.QueryContext(ctx, text, args)
	}
	rows, err := s.stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		return nil, err
	}
	s.reading = true
	return &keptRows{Rows: rows, s: s}, nil
}

func (c *keptConn) drop(e *list.Element) {
	s := c.used.Remove(e).(*keptStatement)
	delete(c.byText, s.text)
	if s.reading {
		s.dropped = true
	} else {
		s.stmt.Close()
	}
}

// Close closes the kept statements first: SQLite does not free a connection that still has any.
func (c *keptConn) Close() error {
	for c.used.Len() > 0 {
		c.drop(c.used.Front())
	}
	return c.readConn.Close()
}

type keptRows struct {
	driver.Rows
	s *keptStatement
}

// Close resets the statement for its next run.
func (r *keptRows) Close() error {
	err := r.Rows.Close()
	r.s.reading = false
	if r.s.dropped {
		r.s.stmt.Close()
	}
	return err
}
