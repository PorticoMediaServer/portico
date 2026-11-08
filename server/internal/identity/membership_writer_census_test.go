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

// membershipWriterExemption is ONE named, exact-match allowance for a native v1
// membership-authority writer that cannot yet use ExecMembershipTx. It is keyed
// to the exact source file AND the exact SQL statement text: a directory, a
// wildcard or a changed statement never matches, and the default (19) census
// still fails on every other file. The SQL stays visible in the writer; hiding,
// splitting or concatenating it is not an exemption.
type membershipWriterExemption struct {
	file      string // exact path relative to the server module root
	statement string // exact unquoted SQL literal
}

var membershipWriterExemptions = []membershipWriterExemption{{
	// server/internal/store/writer/us_policy_effects.go is the US-05 native v1
	// library policy point writer. US-07 (the policy dispatcher task) owns
	// publishing the authority delta and its ONE scoped event in the same
	// transaction as this source update; until US-07 proves that composition,
	// this single write is exempt. Replace this named exception with a positive
	// census entry (ExecMembershipTx admission) once US-07 lands and proves it.
	file: "internal/store/writer/us_policy_effects.go",
	statement: `UPDATE direct_memberships SET allowed_libraries=?,revision=revision+1
WHERE account_id=? AND incarnation=? AND revision=? AND disabled=0
RETURNING incarnation,revision,allowed_libraries`,
}}

func membershipWriterExemptionFor(file, statement string) int {
	for i, exemption := range membershipWriterExemptions {
		if exemption.file == file && exemption.statement == statement {
			return i
		}
	}
	return -1
}

// Read every source file, including inactive build tags. Membership writes must
// use the transaction seam that publishes their actual authority delta, except
// for the exact named exemptions above.
func TestEveryMembershipWriterUsesTheSharedTransactionPath(t *testing.T) {
	root := filepath.Join("..", "..")
	count := 0
	used := make([]bool, len(membershipWriterExemptions))
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
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
			if admitted[literal] {
				return true
			}
			if exemption := membershipWriterExemptionFor(relative, sql); exemption >= 0 {
				used[exemption] = true
				return true
			}
			t.Errorf("%s bypasses ExecMembershipTx", files.Position(literal.Pos()))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, exemption := range membershipWriterExemptions {
		if !used[i] {
			t.Errorf("census exemption for %s no longer matches its exact statement; delete or replace it", exemption.file)
		}
	}
	if want := 19 + len(membershipWriterExemptions); count != want {
		t.Fatalf("membership writer census changed: %d statements, want %d (19 shared-path writers plus %d named exemption(s)); review every writer and update the census", count, want, len(membershipWriterExemptions))
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
