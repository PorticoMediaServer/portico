package identity

import (
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

var membershipUpdateSQL = regexp.MustCompile("(?i)\\bUPDATE\\s+(?:OR\\s+\\w+\\s+)?[\"`\\[]?direct_memberships\\b")

// Read every source file, including inactive build tags. Membership writes must
// use the transaction seam that publishes their actual authority delta.
func TestEveryMembershipWriterUsesTheSharedTransactionPath(t *testing.T) {
	root := filepath.Join("..", "..")
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files := token.NewFileSet()
		file, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			return err
		}
		admitted := map[*ast.BasicLit]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 4 {
				return true
			}
			name := ""
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			}
			if name == "ExecMembershipTx" {
				if literal, ok := call.Args[3].(*ast.BasicLit); ok {
					admitted[literal] = true
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			literal, ok := n.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			sql, err := strconv.Unquote(literal.Value)
			if err != nil || !membershipUpdateSQL.MatchString(sql) {
				return true
			}
			count++
			if !admitted[literal] {
				t.Errorf("%s bypasses ExecMembershipTx", files.Position(literal.Pos()))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 19 {
		t.Fatalf("membership writer census changed: %d statements, want 19; review every writer and update the census", count)
	}
}

var membershipAuthorityAssignment = regexp.MustCompile("(?i)(?:^|,)\\s*[\"`\\[]?(?:role|allowed_libraries|disabled)[\"`\\]]?\\s*=")
var membershipSetClause = regexp.MustCompile(`(?i)^SET\s+`)

func membershipAuthorityTriggers(db *sql.DB) (out []string, err error) {
	rows, err := db.Query(`SELECT name,sql FROM sqlite_schema WHERE type='trigger' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, statement string
		if err := rows.Scan(&name, &statement); err != nil {
			return nil, err
		}
		statement = membershipSQLWithoutComments(statement)
		for _, match := range membershipUpdateSQL.FindAllStringIndex(statement, -1) {
			body := strings.TrimLeft(statement[match[1]:], "\"`] \n\t")
			set := membershipSetClause.FindStringIndex(body)
			if set == nil {
				continue
			}
			body = body[set[1]:]
			if end := strings.Index(strings.ToUpper(body), "WHERE"); end >= 0 {
				body = body[:end]
			}
			if end := strings.Index(body, ";"); end >= 0 {
				body = body[:end]
			}
			if membershipAuthorityAssignment.MatchString(body) {
				out = append(out, name)
				break
			}
		}
	}
	return out, rows.Err()
}

// Comments separate SQL tokens, including when SQLite accepts no whitespace
// around them. Quoted text is kept intact so comment markers in a string or
// identifier never consume the rest of a statement.
func membershipSQLWithoutComments(statement string) string {
	var out strings.Builder
	for i := 0; i < len(statement); {
		switch {
		case strings.HasPrefix(statement[i:], "/*"):
			end := strings.Index(statement[i+2:], "*/")
			out.WriteByte(' ')
			if end < 0 {
				return out.String()
			}
			i += end + 4
		case strings.HasPrefix(statement[i:], "--"):
			end := strings.IndexByte(statement[i+2:], '\n')
			out.WriteByte(' ')
			if end < 0 {
				return out.String()
			}
			i += end + 3
		case statement[i] == '\'' || statement[i] == '"' || statement[i] == '`' || statement[i] == '[':
			quote := statement[i]
			if quote == '[' {
				quote = ']'
			}
			out.WriteByte(statement[i])
			i++
			for i < len(statement) {
				ch := statement[i]
				out.WriteByte(ch)
				i++
				if ch == quote {
					if i < len(statement) && statement[i] == quote {
						out.WriteByte(statement[i])
						i++
						continue
					}
					break
				}
			}
		default:
			out.WriteByte(statement[i])
			i++
		}
	}
	return out.String()
}

func TestNoSchemaTriggerBypassesTheMembershipAuthorityPath(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if writers, err := membershipAuthorityTriggers(db); err != nil || len(writers) != 0 {
		t.Fatalf("SQL-only membership authority writers bypass the shared Go path: %v %v", writers, err)
	}
	var eventTriggers int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='trigger' AND name='membership_authority_changed'`).Scan(&eventTriggers); err != nil || eventTriggers != 0 {
		t.Fatalf("authority emission must not add a schema trigger: %d %v", eventTriggers, err)
	}
	if _, err := db.Exec(`CREATE TRIGGER synthetic_membership_authority AFTER UPDATE OF notes ON direct_memberships BEGIN UPDATE "direct_memberships" SET
 "role"='member' WHERE account_id=NEW.account_id; END`); err != nil {
		t.Fatal(err)
	}
	if writers, err := membershipAuthorityTriggers(db); err != nil || len(writers) != 1 || writers[0] != "synthetic_membership_authority" {
		t.Fatalf("trigger census missed the synthetic authority writer: %v %v", writers, err)
	}
}

func TestMembershipTriggerCensusFindsCommentSeparatedUpdate(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('owner','owner',x'00','owner-main'),('alice','alice',x'00','alice-main');
 UPDATE direct_memberships SET role='admin' WHERE account_id='alice';
 CREATE TRIGGER review_membership_comment AFTER UPDATE OF notes ON direct_memberships BEGIN UPDATE/* valid SQLite comment */direct_memberships SET role='member' WHERE account_id=NEW.account_id; END;
 UPDATE direct_memberships SET notes='changed' WHERE account_id='alice'`); err != nil {
		t.Fatal(err)
	}
	var role string
	if err := db.QueryRow(`SELECT role FROM direct_memberships WHERE account_id='alice'`).Scan(&role); err != nil || role != "member" {
		t.Fatalf("commented trigger did not execute its authority write: %s %v", role, err)
	}
	if writers, err := membershipAuthorityTriggers(db); err != nil || len(writers) != 1 || writers[0] != "review_membership_comment" {
		t.Fatalf("trigger census missed executable commented SQL: %v %v", writers, err)
	}
}
