package httpapi

import "github.com/PorticoMediaServer/portico/server/internal/catalog"

type fileInfoItemResponse struct{}

func (fileInfoItemResponse) JSONSchema() map[string]any {
	return fileInfoUnion(withFileInfoVariant(facetSchema[catalog.Item](), "item"), false)
}
func (fileInfoItemResponse) JSONSchemaForStatus(status int) map[string]any {
	return fileInfoStatusSchema(withFileInfoVariant(facetSchema[catalog.Item](), "item"), status, false)
}

type fileInfoDetailResponse struct{}

func (fileInfoDetailResponse) JSONSchema() map[string]any {
	return fileInfoUnion(withFileInfoVariant(facetSchema[catalog.Detail](), "detail"), false)
}
func (fileInfoDetailResponse) JSONSchemaForStatus(status int) map[string]any {
	return fileInfoStatusSchema(withFileInfoVariant(facetSchema[catalog.Detail](), "detail"), status, false)
}
