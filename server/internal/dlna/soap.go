package dlna

import (
	"encoding/xml"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// UPnP error codes a control request can be answered with.
const (
	errInvalidAction = 401
	errInvalidArgs   = 402
	errNoSuchObject  = 701
	errCannotProcess = 720
)

type soapFault struct {
	code    int
	message string
}

func (f soapFault) Error() string { return f.message }

var errNoObject = soapFault{errNoSuchObject, "No such object"}

// soapCall is one parsed action: its service type, name and arguments.
type soapCall struct {
	service, action string
	args            map[string]string
}

// parseSOAP reads one action from a bounded envelope. encoding/xml expands no
// custom entities and fetches nothing, so the document can only cost its size.
func parseSOAP(body io.Reader) (soapCall, error) {
	decoder := xml.NewDecoder(body)
	call := soapCall{args: map[string]string{}}
	depth, inBody, argument := 0, false, ""
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return soapCall{}, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			switch {
			case depth == 2 && t.Name.Local == "Body":
				inBody = true
			case inBody && depth == 3:
				if call.action != "" {
					return soapCall{}, errors.New("more than one action")
				}
				call.service, call.action = t.Name.Space, t.Name.Local
			case inBody && depth == 4:
				argument = t.Name.Local
				text.Reset()
			case inBody && depth > 4:
				return soapCall{}, errors.New("nested argument")
			}
		case xml.CharData:
			if argument != "" {
				if text.Len()+len(t) > 4096 {
					return soapCall{}, errors.New("argument too long")
				}
				text.Write(t)
			}
		case xml.EndElement:
			if inBody && depth == 4 && argument != "" {
				if len(call.args) >= 16 {
					return soapCall{}, errors.New("too many arguments")
				}
				call.args[argument] = text.String()
				argument = ""
			}
			if depth == 2 && t.Name.Local == "Body" {
				inBody = false
			}
			depth--
		}
	}
	if call.action == "" {
		return soapCall{}, errors.New("no action")
	}
	return call, nil
}

func (s *Server) serveControl(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	var kind string
	for _, v := range services {
		if v.name == name {
			kind = v.kind
		}
	}
	if kind == "" {
		http.NotFound(w, r)
		return
	}
	call, err := parseSOAP(http.MaxBytesReader(w, r.Body, maxControlBytes))
	if err != nil {
		writeFault(w, soapFault{errInvalidAction, "Invalid Action"})
		return
	}
	// SOAPACTION is "service#action"; when a player sends it, it must agree
	// with the envelope.
	if header := strings.Trim(r.Header.Get("SOAPACTION"), `"`); header != "" && header != call.service+"#"+call.action {
		writeFault(w, soapFault{errInvalidAction, "Invalid Action"})
		return
	}
	if call.service != kind {
		writeFault(w, soapFault{errInvalidAction, "Invalid Action"})
		return
	}
	out, err := s.perform(r, call)
	if err != nil {
		var fault soapFault
		if !errors.As(err, &fault) {
			if r.Context().Err() == nil {
				log.Printf("warn: DLNA %s failed: %v", call.action, err)
			}
			fault = soapFault{errCannotProcess, "Cannot process the request"}
		}
		writeFault(w, fault)
		return
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n" + `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:` + call.action + `Response xmlns:u="` + call.service + `">`)
	for i := 0; i+1 < len(out); i += 2 {
		b.WriteString("<" + out[i] + ">" + xmlText(out[i+1]) + "</" + out[i] + ">")
	}
	b.WriteString(`</u:` + call.action + `Response></s:Body></s:Envelope>`)
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.Header().Set("EXT", "")
	_, _ = io.WriteString(w, b.String())
}

// perform runs one action and returns its output arguments as name, value pairs.
func (s *Server) perform(r *http.Request, call soapCall) ([]string, error) {
	switch call.service + "#" + call.action {
	case contentDirectoryType + "#Browse":
		return s.browse(r, call.args)
	case contentDirectoryType + "#GetSearchCapabilities":
		return []string{"SearchCaps", ""}, nil
	case contentDirectoryType + "#GetSortCapabilities":
		return []string{"SortCaps", ""}, nil
	case contentDirectoryType + "#GetSystemUpdateID":
		id, err := s.systemUpdateID(r.Context())
		if err != nil {
			return nil, err
		}
		return []string{"Id", strconv.FormatUint(uint64(id), 10)}, nil
	case connectionManagerType + "#GetProtocolInfo":
		return []string{"Source", sourceProtocols(), "Sink", ""}, nil
	case connectionManagerType + "#GetCurrentConnectionIDs":
		return []string{"ConnectionIDs", "0"}, nil
	case connectionManagerType + "#GetCurrentConnectionInfo":
		if call.args["ConnectionID"] != "0" {
			return nil, soapFault{706, "Invalid connection reference"}
		}
		return []string{"RcsID", "-1", "AVTransportID", "-1", "ProtocolInfo", "", "PeerConnectionManager", "", "PeerConnectionID", "-1", "Direction", "Output", "Status", "OK"}, nil
	case registrarType + "#IsAuthorized", registrarType + "#IsValidated":
		return []string{"Result", "1"}, nil
	case registrarType + "#RegisterDevice":
		return []string{"RegistrationRespMsg", ""}, nil
	}
	return nil, soapFault{errInvalidAction, "Invalid Action"}
}

func writeFault(w http.ResponseWriter, fault soapFault) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="utf-8"?>`+"\n"+`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>`+strconv.Itoa(fault.code)+`</errorCode><errorDescription>`+xmlText(fault.message)+`</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
}
