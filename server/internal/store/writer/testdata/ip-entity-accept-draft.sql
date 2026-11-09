-- Exact E subset needed by IP-09's nonmovie entity provider accept, 8 November
-- 2026. Proposed fixture only; group E is not embedded and this makes no
-- Capture claim. The accepted automatic observation must be retained beside a
-- locked owner value, so the embedded metadata_owner_fields gains its two
-- presence columns here. The IP receipt header, the evidence IP proof tuple and
-- the TV set proof come from the IP-06/IP-07/IP-08 fixtures; the publication,
-- subject-provider, field-origin, entity-ID and term bodies are embedded.
ALTER TABLE metadata_owner_fields ADD COLUMN value_present INTEGER NOT NULL DEFAULT 1 CHECK(typeof(value_present)='integer' AND value_present IN(0,1) AND (value_present=1 OR value=''));

ALTER TABLE metadata_owner_fields ADD COLUMN automatic_present INTEGER NOT NULL DEFAULT 1 CHECK(typeof(automatic_present)='integer' AND automatic_present IN(0,1) AND (automatic_present=1 OR automatic_value=''));
