package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

func (d Dependencies) bundledRcloneRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/storage/rclone/bundled", func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.owner(r); err != nil {
			failure(w, err)
			return
		}
		if r.URL.RawQuery != "" {
			failure(w, requestinput.New("Unexpected query parameters."))
			return
		}
		status := rcloneprogram.Status{Included: true, MinimumVersion: rcloneprogram.MinimumVersion, ErrorCode: "cant_start_rclone"}
		if d.RcloneProgram != nil {
			status = *d.RcloneProgram
		}
		write(w, http.StatusOK, status)
	})
}
