package dlna

import (
	"encoding/xml"
	"strings"
)

const (
	deviceType            = "urn:schemas-upnp-org:device:MediaServer:1"
	contentDirectoryType  = "urn:schemas-upnp-org:service:ContentDirectory:1"
	connectionManagerType = "urn:schemas-upnp-org:service:ConnectionManager:1"
	registrarType         = "urn:microsoft.com:service:X_MS_MediaReceiverRegistrar:1"
)

// service is one UPnP service: its type, id and the path segment shared by its
// description, control and event URLs.
type service struct{ kind, id, name string }

var services = []service{
	{contentDirectoryType, "urn:upnp-org:serviceId:ContentDirectory", "cds"},
	{connectionManagerType, "urn:upnp-org:serviceId:ConnectionManager", "cms"},
	// Xbox and Windows Media Player look for this before they will browse.
	{registrarType, "urn:microsoft.com:serviceId:X_MS_MediaReceiverRegistrar", "mrr"},
}

func xmlText(v string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(v))
	return b.String()
}

func (s *Server) friendlyName() string {
	name := ""
	if s.Name != nil {
		name = strings.TrimSpace(s.Name())
	}
	if name == "" {
		name = "Portico"
	}
	return name
}

func (s *Server) deviceUUID() string {
	id := ""
	if s.ServerID != nil {
		id = s.ServerID()
	}
	return DeviceUUID(id)
}

func (s *Server) deviceDescription() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0" xmlns:dlna="urn:schemas-dlna-org:device-1-0">
<specVersion><major>1</major><minor>0</minor></specVersion>
<device>
<deviceType>` + deviceType + `</deviceType>
<friendlyName>` + xmlText(s.friendlyName()) + `</friendlyName>
<manufacturer>Portico</manufacturer>
<manufacturerURL>https://getportico.tv</manufacturerURL>
<modelDescription>Portico media server</modelDescription>
<modelName>Portico</modelName>
<modelNumber>` + xmlText(s.Version) + `</modelNumber>
<UDN>uuid:` + s.deviceUUID() + `</UDN>
<dlna:X_DLNADOC>DMS-1.50</dlna:X_DLNADOC>
<serviceList>
`)
	for _, v := range services {
		b.WriteString(`<service><serviceType>` + v.kind + `</serviceType><serviceId>` + v.id + `</serviceId><SCPDURL>` + Prefix + `scpd/` + v.name + `.xml</SCPDURL><controlURL>` + Prefix + `control/` + v.name + `</controlURL><eventSubURL>` + Prefix + `event/` + v.name + `</eventSubURL></service>
`)
	}
	b.WriteString(`</serviceList>
<presentationURL>/</presentationURL>
</device>
</root>
`)
	return b.String()
}

// scpd builds a service description from its actions and state variables.
// An action argument is "in:Name:Variable" or "out:Name:Variable".
func scpd(actions map[string][]string, order []string, variables string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<scpd xmlns="urn:schemas-upnp-org:service-1-0">
<specVersion><major>1</major><minor>0</minor></specVersion>
<actionList>
`)
	for _, name := range order {
		b.WriteString("<action><name>" + name + "</name><argumentList>")
		for _, argument := range actions[name] {
			parts := strings.SplitN(argument, ":", 3)
			b.WriteString("<argument><name>" + parts[1] + "</name><direction>" + parts[0] + "</direction><relatedStateVariable>" + parts[2] + "</relatedStateVariable></argument>")
		}
		b.WriteString("</argumentList></action>\n")
	}
	b.WriteString("</actionList>\n<serviceStateTable>\n" + variables + "</serviceStateTable>\n</scpd>\n")
	return b.String()
}

func variable(name, kind string, events bool, allowed ...string) string {
	send := "no"
	if events {
		send = "yes"
	}
	out := `<stateVariable sendEvents="` + send + `"><name>` + name + `</name><dataType>` + kind + `</dataType>`
	if len(allowed) > 0 {
		out += "<allowedValueList>"
		for _, v := range allowed {
			out += "<allowedValue>" + v + "</allowedValue>"
		}
		out += "</allowedValueList>"
	}
	return out + "</stateVariable>\n"
}

var serviceDescriptions = map[string]string{
	"cds.xml": scpd(map[string][]string{
		"Browse": {"in:ObjectID:A_ARG_TYPE_ObjectID", "in:BrowseFlag:A_ARG_TYPE_BrowseFlag", "in:Filter:A_ARG_TYPE_Filter", "in:StartingIndex:A_ARG_TYPE_Index",
			"in:RequestedCount:A_ARG_TYPE_Count", "in:SortCriteria:A_ARG_TYPE_SortCriteria", "out:Result:A_ARG_TYPE_Result", "out:NumberReturned:A_ARG_TYPE_Count",
			"out:TotalMatches:A_ARG_TYPE_Count", "out:UpdateID:A_ARG_TYPE_UpdateID"},
		"GetSearchCapabilities": {"out:SearchCaps:SearchCapabilities"},
		"GetSortCapabilities":   {"out:SortCaps:SortCapabilities"},
		"GetSystemUpdateID":     {"out:Id:SystemUpdateID"},
	}, []string{"Browse", "GetSearchCapabilities", "GetSortCapabilities", "GetSystemUpdateID"},
		variable("A_ARG_TYPE_ObjectID", "string", false)+variable("A_ARG_TYPE_BrowseFlag", "string", false, "BrowseMetadata", "BrowseDirectChildren")+
			variable("A_ARG_TYPE_Filter", "string", false)+variable("A_ARG_TYPE_Index", "ui4", false)+variable("A_ARG_TYPE_Count", "ui4", false)+
			variable("A_ARG_TYPE_SortCriteria", "string", false)+variable("A_ARG_TYPE_Result", "string", false)+variable("A_ARG_TYPE_UpdateID", "ui4", false)+
			variable("SearchCapabilities", "string", false)+variable("SortCapabilities", "string", false)+variable("SystemUpdateID", "ui4", true)),
	"cms.xml": scpd(map[string][]string{
		"GetProtocolInfo":          {"out:Source:SourceProtocolInfo", "out:Sink:SinkProtocolInfo"},
		"GetCurrentConnectionIDs":  {"out:ConnectionIDs:CurrentConnectionIDs"},
		"GetCurrentConnectionInfo": {"in:ConnectionID:A_ARG_TYPE_ConnectionID", "out:RcsID:A_ARG_TYPE_RcsID", "out:AVTransportID:A_ARG_TYPE_AVTransportID", "out:ProtocolInfo:A_ARG_TYPE_ProtocolInfo", "out:PeerConnectionManager:A_ARG_TYPE_ConnectionManager", "out:PeerConnectionID:A_ARG_TYPE_ConnectionID", "out:Direction:A_ARG_TYPE_Direction", "out:Status:A_ARG_TYPE_ConnectionStatus"},
	}, []string{"GetProtocolInfo", "GetCurrentConnectionIDs", "GetCurrentConnectionInfo"},
		variable("SourceProtocolInfo", "string", true)+variable("SinkProtocolInfo", "string", true)+variable("CurrentConnectionIDs", "string", true)+
			variable("A_ARG_TYPE_ConnectionStatus", "string", false, "OK", "ContentFormatMismatch", "InsufficientBandwidth", "UnreliableChannel", "Unknown")+
			variable("A_ARG_TYPE_ConnectionManager", "string", false)+variable("A_ARG_TYPE_Direction", "string", false, "Input", "Output")+
			variable("A_ARG_TYPE_ProtocolInfo", "string", false)+variable("A_ARG_TYPE_ConnectionID", "i4", false)+
			variable("A_ARG_TYPE_AVTransportID", "i4", false)+variable("A_ARG_TYPE_RcsID", "i4", false)),
	"mrr.xml": scpd(map[string][]string{
		"IsAuthorized":   {"in:DeviceID:A_ARG_TYPE_DeviceID", "out:Result:A_ARG_TYPE_Result"},
		"IsValidated":    {"in:DeviceID:A_ARG_TYPE_DeviceID", "out:Result:A_ARG_TYPE_Result"},
		"RegisterDevice": {"in:RegistrationReqMsg:A_ARG_TYPE_RegistrationReqMsg", "out:RegistrationRespMsg:A_ARG_TYPE_RegistrationRespMsg"},
	}, []string{"IsAuthorized", "IsValidated", "RegisterDevice"},
		variable("A_ARG_TYPE_DeviceID", "string", false)+variable("A_ARG_TYPE_Result", "int", false)+
			variable("A_ARG_TYPE_RegistrationReqMsg", "bin.base64", false)+variable("A_ARG_TYPE_RegistrationRespMsg", "bin.base64", false)+
			variable("AuthorizationGrantedUpdateID", "ui4", true)+variable("AuthorizationDeniedUpdateID", "ui4", true)+
			variable("ValidationSucceededUpdateID", "ui4", true)+variable("ValidationRevokedUpdateID", "ui4", true)),
}
