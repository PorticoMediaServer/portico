package catalogtest

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// SonicEmbedding declares normalized stored numeric facts for selection tests.
// It does not run a model or qualify source scanning/inference.
func (c *Catalog) SonicEmbedding(item Item, x, y float32) {
	c.T.Helper()
	values := make([]float32, sonic.Dimensions)
	values[0] = x
	values[1] = y
	vector, err := sonic.Normalize(values)
	if err != nil {
		c.T.Fatal(err)
	}
	data, err := vector.Bytes()
	if err != nil {
		c.T.Fatal(err)
	}
	id := fmt.Sprint(item.ID)
	c.Exec(`UPDATE catalog_assets SET audio_codec='flac',duration=2 WHERE id=?`, item.Asset)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT ?,src.id,a.token,src.incarnation,a.path,'r','{}',a.size,a.modified_ns FROM catalog_assets a JOIN catalog_asset_links link ON link.asset_id=a.id JOIN catalog_entities item ON item.id=link.entity_id JOIN catalog_libraries library ON library.id=item.library_id JOIN library_sources src ON src.library_id=library.library_id WHERE a.id=? LIMIT 1`, id, item.Asset)
	c.Exec(`INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms) SELECT ?,?,o.asset_id,o.revision,src.incarnation,src.generation,1,'sonic','portico-analysis-3:sonic:fixture','fixture','fixture',2000000,'{}',1 FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id WHERE o.id=?`, id, id, id)
	c.Exec(`INSERT INTO analysis_heads(object_id,source_revision,stage,result_id) VALUES(?,'r','sonic',?)`, id, id)
	c.Exec(`INSERT INTO music_sonic_embeddings(result_id,entity_id,source_start_us,source_end_us,model_id,model_version,model_sha256,dimensions,vector) VALUES(?,?,0,2000000,?,?,?,200,?)`, id, item.ID, sonic.ModelID, sonic.ModelVersion, sonic.ModelSHA256, data)
}
