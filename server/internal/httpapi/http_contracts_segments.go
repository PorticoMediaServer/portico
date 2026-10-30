package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	mediaanalysis "github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

func registerSegmentHTTPContracts(registry *apikit.Registry) error {
	errors := []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}
	// internal/httpapi/segments.go
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[mediaanalysis.SegmentStatus]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_segments", Method: "GET", Path: "/v1/admin/segments", Summary: "Read intro and credits detection status", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: errors}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[mediaanalysis.SegmentTitlePage]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_segments_titles", Method: "GET", Path: "/v1/admin/segments/titles", Summary: "List a library's shows and films for intro and credits review", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"libraryId", "cursor", "limit"}, QueryIntegers: limitQuery(50, 100), Errors: errors}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[mediaanalysis.SegmentTitleReview]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_segments_titles_id", Method: "GET", Path: "/v1/admin/segments/titles/{id}", Summary: "Read one show's or film's detected intros and credits", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: errors}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[SegmentApproveRequest, LiveOwnerResult[SegmentChangeResult]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_segments_titles_id_approve", Method: "POST", Path: "/v1/admin/segments/titles/{id}/approve", Summary: "Approve a show's or season's detected segments", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 2097152, Status: 200, Errors: errors}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[SegmentChangeResult]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_segments_titles_id_redetect", Method: "POST", Path: "/v1/admin/segments/titles/{id}/redetect", Summary: "Detect a show's or film's intros and credits again", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: errors}); err != nil {
		return err
	}
	return nil
}
