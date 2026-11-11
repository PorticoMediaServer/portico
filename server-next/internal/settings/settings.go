// Package settings holds the owner's settings documents. Each area declares the documents it
// owns with Define; any package reads one with Get, which answers from memory, and may Watch
// it to apply a change at once, so no setting needs a restart and no request reads the table.
//
// A document is one row of the settings table, kept as JSON and saved whole. Its revision is
// the ETag the owner's devices send back, so a save made from a page that is out of date is
// refused instead of overwriting a change the owner did not see.
package settings

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Doc is one declared document. T is its Go shape: a struct whose fields carry json names.
// A string field tagged `settings:"secret"` is sealed when stored and is never read back
// over the wire: the answer says only whether it is set.
type Doc[T any] struct{ def *definition }

type definition struct {
	name     string
	typ      reflect.Type
	defaults any // a T
	check    func(any) error
	secrets  []string // the json names of the secret fields
}

var (
	definedMu sync.Mutex
	defined   = map[string]*definition{}
)

// Define declares a document. defaults is the value it has until the owner changes it, and
// must work as it is. check refuses a value the server cannot act on (return an *Invalid
// naming the field); it may also tidy the value, such as trimming spaces. Define is called
// from a package-level var, once per name.
func Define[T any](name string, defaults T, check func(*T) error) *Doc[T] {
	typ := reflect.TypeFor[T]()
	if typ.Kind() != reflect.Struct {
		panic("settings: a document is a struct: " + name)
	}
	def := &definition{name: name, typ: typ, defaults: defaults}
	if check != nil {
		def.check = func(v any) error { return check(v.(*T)) }
	}
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Tag.Get("settings") != "secret" {
			continue
		}
		if f.Type.Kind() != reflect.String {
			panic("settings: a secret is a string: " + name + "." + f.Name)
		}
		def.secrets = append(def.secrets, jsonName(f))
	}
	definedMu.Lock()
	defer definedMu.Unlock()
	if defined[name] != nil {
		panic("settings: two documents named " + name)
	}
	defined[name] = def
	return &Doc[T]{def}
}

func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if i := slices.Index([]byte(tag), ','); i >= 0 {
		tag = tag[:i]
	}
	if tag == "" {
		return f.Name
	}
	return tag
}

// Name is the document's name: its address under /v1/admin/settings and its settings row.
func (d *Doc[T]) Name() string { return d.def.name }

// Get returns the document as it is now. Treat it as read-only: a slice or map in it is
// shared with every other reader. With no Settings (a package's own tests) it is the defaults.
func (d *Doc[T]) Get(s *Settings) T {
	if s == nil {
		return d.def.defaults.(T)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.docs[d.def.name].value.(T)
}

// Watch calls fn with the new value after every save, once the save has committed. fn runs
// before the save is answered, so it must be quick: hand anything slow to a goroutine. It
// must not save a settings document itself: saves wait for one another. With no Settings
// there is nothing to watch.
func (d *Doc[T]) Watch(s *Settings, fn func(T)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.docs[d.def.name]
	doc.watchers = append(doc.watchers, func(v any) { fn(v.(T)) })
}

// Invalid refuses one field of a document. Field is the field's json name, dotted for a
// field inside another.
type Invalid struct {
	Field, Message string
}

func (e *Invalid) Error() string { return e.Field + ": " + e.Message }

// ErrStale: the document was saved since the revision the caller read.
var ErrStale = errors.New("the settings were changed since they were read")

// ErrNoDocument: no document has that name.
var ErrNoDocument = errors.New("no such settings document")

// Sealer seals and opens secrets with the server's key. belongsTo binds a sealed value to the
// field it was sealed for, so it cannot be moved to another field and opened there.
type Sealer interface {
	Seal(belongsTo string, plain []byte) []byte
	Open(belongsTo string, sealed []byte) ([]byte, error)
}

// Settings is one server's documents, held in memory.
type Settings struct {
	db     *db.DB
	sealer Sealer
	mu     sync.RWMutex
	docs   map[string]*loaded
	// saving puts saves of one document in order, so watchers see them in the order they
	// committed.
	saving sync.Mutex
}

type loaded struct {
	def      *definition
	value    any // a T
	revision int64
	watchers []func(any)
}

// Open reads every declared document. A stored document this build cannot read, or one its
// check now refuses, is used at its defaults and the problem is returned in damaged, so the
// server still starts; the owner's next save replaces it. sealer may be nil while no
// document has a secret.
func Open(ctx context.Context, d *db.DB, sealer Sealer) (s *Settings, damaged []error, err error) {
	s = &Settings{db: d, sealer: sealer, docs: map[string]*loaded{}}
	definedMu.Lock()
	defs := make([]*definition, 0, len(defined))
	for _, def := range defined {
		defs = append(defs, def)
	}
	definedMu.Unlock()
	for _, def := range defs {
		raw, revision, err := d.SettingDocument(ctx, def.name)
		if err != nil {
			return nil, nil, err
		}
		value, err := s.decode(def, raw)
		if err != nil {
			damaged = append(damaged, fmt.Errorf("the %s settings: %w", def.name, err))
			value = s.copyOf(def, def.defaults)
		}
		s.docs[def.name] = &loaded{def: def, value: value, revision: revision}
	}
	return s, damaged, nil
}

// decode reads a stored document over its defaults, so a field this build added since it was
// saved has its default, and opens its secrets.
func (s *Settings) decode(def *definition, raw string) (any, error) {
	ptr := reflect.New(def.typ)
	ptr.Elem().Set(reflect.ValueOf(s.copyOf(def, def.defaults)))
	if raw == "" {
		return ptr.Elem().Interface(), nil
	}
	if err := json.Unmarshal([]byte(raw), ptr.Interface()); err != nil {
		return nil, err
	}
	for _, name := range def.secrets {
		field := secretField(ptr.Elem(), name)
		sealed, err := base64.StdEncoding.DecodeString(field.String())
		var plain []byte
		if err == nil && field.String() != "" && s.sealer != nil {
			plain, err = s.sealer.Open(def.name+"."+name, sealed)
		}
		// A secret this server cannot open (its key file was replaced) is not set: the owner
		// enters it again.
		if err != nil || s.sealer == nil {
			plain = nil
		}
		field.SetString(string(plain))
	}
	if def.check != nil {
		if err := def.check(ptr.Interface()); err != nil {
			return nil, err
		}
	}
	return ptr.Elem().Interface(), nil
}

// copyOf returns a value of the document that shares nothing with v.
func (s *Settings) copyOf(def *definition, v any) any {
	raw, _ := json.Marshal(v)
	ptr := reflect.New(def.typ)
	json.Unmarshal(raw, ptr.Interface())
	// Secrets are not left out of json.Marshal on a plain struct, so they copy too.
	return ptr.Elem().Interface()
}

func secretField(v reflect.Value, name string) reflect.Value {
	for i := range v.NumField() {
		if jsonName(v.Type().Field(i)) == name {
			return v.Field(i)
		}
	}
	panic("settings: no field " + name)
}

// Document is one document as the owner's pages read it.
type Document struct {
	Name string
	// Values are the document's fields, secrets left out.
	Values json.RawMessage
	// Defaults are what each field is until the owner changes it, so a page can say a value
	// is changed and offer Reset.
	Defaults json.RawMessage
	// Secrets says, for each secret field, whether one is set.
	Secrets  map[string]bool
	Revision int64
}

// Names lists the documents there are, in name order.
func (s *Settings) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.docs))
	for name := range s.docs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Read returns one document by name.
func (s *Settings) Read(name string) (Document, error) {
	s.mu.RLock()
	doc := s.docs[name]
	var value any
	var revision int64
	if doc != nil {
		value, revision = doc.value, doc.revision
	}
	s.mu.RUnlock()
	if doc == nil {
		return Document{}, ErrNoDocument
	}
	return s.document(doc.def, value, revision), nil
}

func (s *Settings) document(def *definition, value any, revision int64) Document {
	out := Document{Name: def.name, Revision: revision, Secrets: map[string]bool{}}
	v := reflect.ValueOf(value)
	for _, name := range def.secrets {
		out.Secrets[name] = secretField(v, name).String() != ""
	}
	out.Values = withoutSecrets(def, value)
	out.Defaults = withoutSecrets(def, def.defaults)
	return out
}

func withoutSecrets(def *definition, value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	if len(def.secrets) == 0 {
		return raw
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	for _, name := range def.secrets {
		delete(fields, name)
	}
	raw, _ = json.Marshal(fields)
	return raw
}

// Write saves a change to one document made by a person (actorID, for the activity record).
// change is JSON laid over the document as it is now: a field left out keeps its value, so a
// page that saves one row sends one field, and a secret left out stays as it is (an empty
// string removes it). match is the revision the caller read, or -1 to save over whatever is
// there. The document is checked whole before it is saved, and its watchers are told before
// Write returns.
func (s *Settings) Write(ctx context.Context, actorID int64, name string, change json.RawMessage, match int64, now time.Time) (Document, error) {
	s.saving.Lock()
	defer s.saving.Unlock()
	s.mu.RLock()
	doc := s.docs[name]
	var current any
	var revision int64
	if doc != nil {
		current, revision = doc.value, doc.revision
	}
	s.mu.RUnlock()
	if doc == nil {
		return Document{}, ErrNoDocument
	}
	if match >= 0 && match != revision {
		return Document{}, ErrStale
	}
	def := doc.def
	ptr := reflect.New(def.typ)
	ptr.Elem().Set(reflect.ValueOf(s.copyOf(def, current)))
	if err := overlay(ptr.Elem(), change); err != nil {
		return Document{}, err
	}
	if def.check != nil {
		if err := def.check(ptr.Interface()); err != nil {
			return Document{}, err
		}
	}
	value := ptr.Elem().Interface()
	stored, err := s.encode(def, value)
	if err != nil {
		return Document{}, err
	}
	revision, err = s.db.SaveSettingDocument(ctx, actorID, name, stored, match, now)
	if errors.Is(err, db.ErrStale) {
		return Document{}, ErrStale
	}
	if err != nil {
		return Document{}, err
	}
	s.mu.Lock()
	doc.value, doc.revision = value, revision
	watchers := slices.Clone(doc.watchers)
	s.mu.Unlock()
	for _, fn := range watchers {
		fn(value)
	}
	return s.document(def, value, revision), nil
}

// encode is the document as it is stored: secrets sealed, or left out when not set.
func (s *Settings) encode(def *definition, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(def.secrets) == 0 {
		return string(raw), err
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	v := reflect.ValueOf(value)
	for _, name := range def.secrets {
		plain := secretField(v, name).String()
		if plain == "" {
			delete(fields, name)
			continue
		}
		if s.sealer == nil {
			return "", &Invalid{Field: name, Message: "This server cannot keep a secret yet."}
		}
		sealed := base64.StdEncoding.EncodeToString(s.sealer.Seal(def.name+"."+name, []byte(plain)))
		fields[name], _ = json.Marshal(sealed)
	}
	raw, err = json.Marshal(fields)
	return string(raw), err
}

// overlay lays a change over a document one field at a time. A field that is sent replaces
// what was there whole, so a list or a map is the one sent and never a mix of it and the old
// one. A name the document does not have is refused: a mistyped field would otherwise be
// answered as saved with nothing changed.
func overlay(v reflect.Value, change json.RawMessage) error {
	const notAValue = "This is not a value this setting takes."
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(change, &fields); err != nil || fields == nil {
		return &Invalid{Message: notAValue}
	}
	for name, raw := range fields {
		var field reflect.Value
		for i := range v.NumField() {
			if f := v.Type().Field(i); f.IsExported() && jsonName(f) == name && name != "-" {
				field = v.Field(i)
			}
		}
		if !field.IsValid() {
			return &Invalid{Field: name, Message: "This server has no such setting."}
		}
		fresh := reflect.New(field.Type())
		if err := json.Unmarshal(raw, fresh.Interface()); err != nil || string(raw) == "null" {
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &typeErr) && typeErr.Field != "" {
				name += "." + typeErr.Field
			}
			return &Invalid{Field: name, Message: notAValue}
		}
		field.Set(fresh.Elem())
	}
	return nil
}
