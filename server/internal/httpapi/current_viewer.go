package httpapi

import "net/http"

func (d Dependencies) currentViewer(w http.ResponseWriter, r *http.Request) {
	p, err := d.strictPrincipalFor(r)
	if err != nil {
		failure(w, err)
		return
	}
	write(w, 200, map[string]any{"viewer": p.Viewer})
}
