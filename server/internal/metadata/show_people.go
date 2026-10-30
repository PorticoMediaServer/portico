package metadata

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// showPeopleSQL rebuilds one show's cast and crew (show_people_credits) from its
// credits field, and makes sure each credited person has a catalog_people row
// under the same identity key the item credits use (provider:id, else
// name:<folded name>), so a TMDB-credited actor is the same /v1/people person
// on a show and in a movie. ?1 is the show's entity id.
const showPeopleSQL = `DELETE FROM show_people_credits WHERE show_id=?1;
INSERT OR IGNORE INTO show_people_credits(show_id,identity_key,provider,credit_id,name,role,department,ordinal)
 SELECT sf.target_id,
  CASE WHEN COALESCE(json_extract(j.value,'$.id'),'')<>'' THEN sf.provider||':'||json_extract(j.value,'$.id') ELSE COALESCE((SELECT p.identity_key FROM catalog_people_aliases a JOIN catalog_people p ON p.id=a.person_id WHERE a.alias='name:'||lower(trim(json_extract(j.value,'$.name')))),'name:'||lower(trim(json_extract(j.value,'$.name')))) END,
  sf.provider,COALESCE(json_extract(j.value,'$.id'),''),trim(json_extract(j.value,'$.name')),COALESCE(json_extract(j.value,'$.role'),''),COALESCE(json_extract(j.value,'$.department'),''),CAST(j.key AS INTEGER)
 FROM screen_metadata_fields sf,json_each(CASE WHEN json_valid(sf.value) AND json_type(sf.value)='array' THEN sf.value ELSE '[]' END) j
 WHERE sf.target_kind='show' AND sf.target_id=?1 AND sf.field IN('credits','creditsOnline') AND CAST(j.key AS INTEGER)<128 AND trim(COALESCE(json_extract(j.value,'$.name'),''))<>'';
INSERT OR IGNORE INTO catalog_people(token,identity_key,name,sort_name,provider_ids)
 SELECT lower(hex(randomblob(16))),identity_key,name,lower(trim(name)),CASE WHEN credit_id<>'' THEN json_object(provider,credit_id) ELSE '{}' END
 FROM show_people_credits WHERE show_id=?1 GROUP BY identity_key;`

// syncShowPeople rebuilds one show's people inside the caller's transaction. It
// is called wherever the show's credits field changes: a provider publication,
// an NFO publication, and a repair that clears fields. show is its public id.
func syncShowPeople(ctx context.Context, tx *sql.Tx, show string) error {
	id, err := entityid.Resolve(ctx, tx, show)
	if errors.Is(err, entityid.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, showPeopleSQL, id)
	return err
}
