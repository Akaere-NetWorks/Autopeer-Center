ALTER TABLE peers
    ALTER COLUMN wg_managed SET DEFAULT true;

UPDATE peers
SET created_at = COALESCE(NULLIF(updated_at, '0001-01-01 00:00:00+00'::timestamptz), now())
WHERE created_at IS NULL
   OR created_at < '2000-01-01 00:00:00+00'::timestamptz;

UPDATE peers
SET updated_at = created_at
WHERE updated_at IS NULL
   OR updated_at < '2000-01-01 00:00:00+00'::timestamptz;

UPDATE peers
SET wg_managed = true
WHERE wg_managed = false
  AND COALESCE(bgp_proto_name, '') = ''
  AND COALESCE(bird_config_filename, '') = '';
