package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This is the local operation-result SHAPE for domain alignment (including
// library retirement), not a generic command, authority issuer or callback API.
// IA need not import it. UO's finite codec binds its original typed command.
type uoOperationResult struct {
	status                                    uint8 // 1 complete; 2 accepted-pending; 3 partial; 4 terminal failure
	revision, cursor, done, failed, appliedMS int64
	target                                    [16]byte
}
type uoOperationScope struct{ authority, account, profile, membership, lifetime string }
type uoOperationReceipt struct {
	scope            uoOperationScope
	identity         CommandIdentity
	request          RequestDigest
	kind             CommandID
	nowMS, expiresMS int64
}

const uoOperationPayloadBytes = 139

func (q uoOperationReceipt) keys() (scope, key string, err error) {
	switch q.kind {
	case BeginPersonalJob, StepPersonalJob, AdvanceNoticeOperation, ClaimOperationalJob, RenewOperationalJob, CompleteOperationalJob, ReclaimOperationalJob:
	default:
		return "", "", ErrInvalid
	}
	if q.scope.authority != "local" && q.scope.authority != "hosted" || q.kind < 600 || q.kind > 699 || q.identity.Incarnation == [16]byte{} || q.identity.OperationID == [16]byte{} || q.identity.Sequence <= 0 || q.request == [32]byte{} || q.nowMS <= 0 {
		return "", "", ErrInvalid
	}
	var raw []byte
	for i, s := range []string{q.scope.authority, q.scope.account, q.scope.profile, q.scope.membership, q.scope.lifetime} {
		limit := 128
		if i >= 3 {
			limit = 32
		}
		if len(s) == 0 || len(s) > limit || i >= 3 && len(s) != 32 || !utf8.ValidString(s) || bytes.IndexByte([]byte(s), 0) >= 0 {
			return "", "", ErrInvalid
		}
		raw = binary.BigEndian.AppendUint32(raw, uint32(len(s)))
		raw = append(raw, s...)
	}
	scope = "uo1_" + base64.RawURLEncoding.EncodeToString(raw)
	key = hex.EncodeToString(q.identity.Incarnation[:]) + "." + strconv.FormatInt(q.identity.Sequence, 10) + "." + hex.EncodeToString(q.identity.OperationID[:])
	return scope, key, nil
}
func encodeUOOperationResult(q uoOperationReceipt, r uoOperationResult) (string, ResultDigest, error) {
	scope, _, err := q.keys()
	if err != nil {
		return "", ResultDigest{}, err
	}
	if r.status < 1 || r.status > 4 || r.revision < 0 || r.cursor < 0 || r.done < 0 || r.failed < 0 || r.appliedMS <= 0 {
		return "", ResultDigest{}, ErrInvalid
	}
	var p []byte
	p = append(p, 'U', 'O', 'R', 'E', 'S', 1, 0, 0)
	p = binary.BigEndian.AppendUint16(p, uint16(q.kind))
	p = append(p, q.identity.Incarnation[:]...)
	p = binary.BigEndian.AppendUint64(p, uint64(q.identity.Sequence))
	p = append(p, q.identity.OperationID[:]...)
	p = append(p, q.request[:]...)
	p = append(p, r.status)
	for _, n := range []int64{r.revision, r.cursor, r.done, r.failed, r.appliedMS} {
		p = binary.BigEndian.AppendUint64(p, uint64(n))
	}
	p = append(p, r.target[:]...)
	if len(p) != uoOperationPayloadBytes {
		return "", ResultDigest{}, ErrInvalid
	}
	digest := uoOperationChecksum(scope, p)
	return base64.RawURLEncoding.EncodeToString(p) + "." + hex.EncodeToString(digest[:]), ResultDigest(digest), nil
}
func decodeUOOperationResult(q uoOperationReceipt, encoded string) (uoOperationResult, error) {
	var r uoOperationResult
	scope, _, scopeErr := q.keys()
	if scopeErr != nil {
		return r, ErrResolveRequired
	}
	const prefix = "UORES\x01\x00\x00"
	bound := base64.RawURLEncoding.EncodedLen(uoOperationPayloadBytes)
	if len(encoded) != bound+1+64 || encoded[bound] != '.' {
		return r, ErrResolveRequired
	}
	p, err := base64.RawURLEncoding.Strict().DecodeString(encoded[:bound])
	if err != nil || len(p) != uoOperationPayloadBytes || string(p[:8]) != prefix {
		return r, ErrResolveRequired
	}
	digest := uoOperationChecksum(scope, p)
	if encoded[bound+1:] != hex.EncodeToString(digest[:]) {
		return r, ErrResolveRequired
	}
	if CommandID(binary.BigEndian.Uint16(p[8:10])) != q.kind || !bytes.Equal(p[10:26], q.identity.Incarnation[:]) || binary.BigEndian.Uint64(p[26:34]) != uint64(q.identity.Sequence) || !bytes.Equal(p[34:50], q.identity.OperationID[:]) || !bytes.Equal(p[50:82], q.request[:]) {
		return r, ErrConflict
	}
	r.status = p[82]
	dst := []*int64{&r.revision, &r.cursor, &r.done, &r.failed, &r.appliedMS}
	for i, d := range dst {
		n := binary.BigEndian.Uint64(p[83+8*i : 91+8*i])
		if n > uint64(^uint64(0)>>1) {
			return uoOperationResult{}, ErrResolveRequired
		}
		*d = int64(n)
	}
	copy(r.target[:], p[123:139])
	if _, _, err := encodeUOOperationResult(q, r); err != nil {
		return uoOperationResult{}, ErrResolveRequired
	}
	return r, nil
}

// The lookup key and checksum both bind the exact current lifetime scope. A
// payload copied between principals is not a valid saved result there, even if
// its original command identity/request happened to be repeated by a caller.
func uoOperationChecksum(scope string, p []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte("portico UO operation result v1"))
	h.Write(binary.BigEndian.AppendUint32(nil, uint32(len(scope))))
	h.Write([]byte(scope))
	h.Write(p)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// Fresh-only persistence under W's SAME authorized/classified command frame.
// W must reserve/refund exact receipt quota and own the retained owner/floor.
// These helpers cannot classify a missing or expired receipt as a fresh retry.
func persistUOOperationResult(ctx context.Context, turn *commandTurn, q uoOperationReceipt, r uoOperationResult) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if q.expiresMS <= q.nowMS || r.appliedMS != q.nowMS {
		return out, ErrInvalid
	}
	scope, key, err := q.keys()
	if err != nil {
		return out, err
	}
	encoded, digest, err := encodeUOOperationResult(q, r)
	if err != nil {
		return out, err
	}
	if _, err = wc008DraftInteger(ctx, turn, uoOperationSave, scope, key, hex.EncodeToString(q.request[:]), encoded, q.expiresMS); err != nil {
		return out, err
	}
	return digest, turn.checkLifetimes(ctx)
}
func restoreUOOperationResult(ctx context.Context, turn *commandTurn, q uoOperationReceipt) (out uoOperationResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	scope, key, err := q.keys()
	if err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoOperationOctets, hex.EncodeToString(q.request[:]), scope, key)
	if err != nil {
		return out, err
	}
	var n, expiry int64
	var requestMatches int64
	if err = row.Scan(&n, &expiry, &requestMatches); err != nil {
		return out, err
	}
	if n != int64(base64.RawURLEncoding.EncodedLen(uoOperationPayloadBytes)+65) || expiry <= q.nowMS {
		return out, ErrResolveRequired
	}
	if requestMatches != 1 {
		return out, ErrConflict
	}
	row, err = wc008DraftQuery(ctx, turn, uoOperationPoint, scope, key)
	if err != nil {
		return out, err
	}
	var encoded string
	if err = row.Scan(&encoded); err != nil {
		return out, err
	}
	if int64(len(encoded)) != n {
		return out, ErrResolveRequired
	}
	return decodeUOOperationResult(q, encoded)
}

const (
	uoOperationSave   stmt.ID = "uo.operation.receipt.save"
	uoOperationOctets stmt.ID = "uo.operation.receipt.octets"
	uoOperationPoint  stmt.ID = "uo.operation.receipt.point"
)

func uoOperationDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	d := func(id stmt.ID, kind stmt.Kind, query string, p ...stmt.Param) stmt.Definition {
		return stmt.Definition{ID: id, Kind: kind, Owner: "store/writer UO operation receipts", SQL: query, Params: p, CallSites: []string{"store/writer/uo_operation_receipts.go"}, Plan: stmt.Plan{Hot: true}}
	}
	return []stmt.Definition{
		d(uoOperationSave, stmt.Write, `INSERT INTO console_receipts(scope,key,digest,result,expires_ms) VALUES(?,?,?,?,?) RETURNING expires_ms`, s("scope"), s("key"), s("digest"), s("result"), i("expiry")),
		d(uoOperationOctets, stmt.Read, `SELECT octet_length(result),expires_ms,(digest=?) FROM console_receipts WHERE scope=? AND key=?`, s("digest"), s("scope"), s("key")),
		d(uoOperationPoint, stmt.Read, `SELECT result FROM console_receipts WHERE scope=? AND key=?`, s("scope"), s("key")),
	}
}
