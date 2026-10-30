package social

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

// The controller wire methods reflect the exact runtime checks in
// cast_controller.go. Only constraints the handlers actually enforce are added;
// other fields keep the reflected shape.

func (CastControllerRegister) JSONSchema() map[string]any {
	type wire CastControllerRegister
	s, e := apikit.Schema(reflect.TypeFor[wire]())
	if e != nil {
		panic(e)
	}
	p := s["properties"].(map[string]any)
	p["bootstrapId"].(map[string]any)["pattern"] = `^[A-Za-z0-9_-]{1,128}$` // ValidID(req.BootstrapID)
	// len(req.ControlToken) == 48 counts UTF-8 bytes and stays handler-owned.
	// JSON length counts characters, so this is only a conservative bound.
	p["controlToken"].(map[string]any)["minLength"] = 1
	p["controlToken"].(map[string]any)["maxLength"] = 48
	return s
}

func (CastControllerValidation) JSONSchema() map[string]any {
	type wire CastControllerValidation
	s, e := apikit.Schema(reflect.TypeFor[wire]())
	if e != nil {
		panic(e)
	}
	p := s["properties"].(map[string]any)
	p["attemptId"].(map[string]any)["pattern"] = `^[A-Za-z0-9_-]{1,128}$` // ValidID(req.AttemptID)
	binding := p["binding"].(map[string]any)["properties"].(map[string]any)
	binding["id"].(map[string]any)["pattern"] = `^[A-Za-z0-9_-]{1,128}$` // ValidID(req.Binding.ID)
	return s
}

func (CastControllerCheck) JSONSchema() map[string]any {
	type wire CastControllerCheck
	s, e := apikit.Schema(reflect.TypeFor[wire]())
	if e != nil {
		panic(e)
	}
	p := s["properties"].(map[string]any)
	p["bindingId"].(map[string]any)["pattern"] = `^[A-Za-z0-9_-]{1,128}$` // ValidID(req.BindingID)
	// len(req.ControlToken) == 48 counts UTF-8 bytes and stays handler-owned.
	// JSON length counts characters, so this is only a conservative bound.
	p["controlToken"].(map[string]any)["minLength"] = 1
	p["controlToken"].(map[string]any)["maxLength"] = 48
	p["attemptId"].(map[string]any)["pattern"] = `^$|^[A-Za-z0-9_-]{1,128}$` // ValidID(req.AttemptID) in the present-ticket branch; an empty pair is accepted
	// The 43..256-byte and pair rules are handler-owned; JSON length counts characters.
	p["ticket"].(map[string]any)["maxLength"] = 256
	return s
}

func (CastControllerResponse) JSONSchema() map[string]any {
	type wire CastControllerResponse
	s, e := apikit.Schema(reflect.TypeFor[wire]())
	if e != nil {
		panic(e)
	}
	p := s["properties"].(map[string]any)
	binding := p["binding"].(map[string]any)["properties"].(map[string]any)
	binding["id"].(map[string]any)["pattern"] = `^[A-Za-z0-9_-]{1,128}$` // generated cct_ binding id
	p["attemptId"].(map[string]any)["pattern"] = `^[A-Za-z0-9_-]{1,128}$` // echoed validated attempt id
	p["ticket"].(map[string]any)["minLength"] = 43
	p["ticket"].(map[string]any)["maxLength"] = 43 // identity.Token() output
	return s
}
