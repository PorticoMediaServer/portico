package receiver

// ApplicationID is Portico's registered Google Cast application, set by -X in a
// controlled build. Empty ordinary builds do not offer Cast. Owners register nothing.
var ApplicationID string

// Origin hosts the one receiver page registered with Google. The receiver code
// talks to the HTTPS server named by its paired sender, not to this host's API.
const Origin = "https://cast.getportico.tv"
