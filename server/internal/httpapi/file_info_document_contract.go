package httpapi

import "github.com/PorticoMediaServer/portico/server/internal/catalog"

type fileInfoItemResponse struct{}

func (fileInfoItemResponse) JSONSchema() map[string]any {
	return fileInfoUnion(facetSchema[catalog.Item](), false)
}
func (fileInfoItemResponse) JSONSchemaForStatus(status int) map[string]any {
	return fileInfoStatusSchema(facetSchema[catalog.Item](), status, false)
}

type fileInfoDetailResponse struct{}

func (fileInfoDetailResponse) JSONSchema() map[string]any {
	return fileInfoUnion(facetSchema[catalog.Detail](), false)
}
func (fileInfoDetailResponse) JSONSchemaForStatus(status int) map[string]any {
	return fileInfoStatusSchema(facetSchema[catalog.Detail](), status, false)
}
