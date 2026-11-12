package tmdb

// ProjectToken is Portico's own TMDB read access token, built into the server so a new server
// looks titles up without the owner doing anything. It is not a secret: TMDB issues read
// tokens for exactly this, and other media servers publish theirs the same way (definition
// area 4, rule 8). An owner who enters their own key in Settings › Metadata uses that instead.
// No route, settings document or log line carries it: it is sent to TMDB alone, as a request's
// Authorization header.
const ProjectToken = "eyJhbGciOiJIUzI1NiJ9." +
	"eyJhdWQiOiI2OGE5Y2M5ZGNhNjJkMDg2ZWEzZWQwNTg1YmMwZDcyYyIsIm5iZiI6MTc3NzU4Njc3MS44MzIsInN1YiI6IjY5ZjNkMjUzMTA2ODk0N2I0NmZiZDUwZiIsInNjb3BlcyI6WyJhcGlfcmVhZCJdLCJ2ZXJzaW9uIjoxfQ." +
	"AmRWVmPw6MJOYp5jv83nvOYJUWXRx1YsoEtUDDQht_8"
