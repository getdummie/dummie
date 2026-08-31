CREATE OR REPLACE FUNCTION osimages_forbid_update() RETURNS trigger AS $$
BEGIN
    IF OLD.soft_deleted_at IS NOT NULL THEN
        RAISE EXCEPTION 'a withdrawn os image cannot be changed';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.name IS DISTINCT FROM OLD.name
        OR NEW.source IS DISTINCT FROM OLD.source
        OR NEW.oci_ref IS DISTINCT FROM OLD.oci_ref
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'the identity and source of an os image cannot be changed';
    END IF;
    IF OLD.status = 'ready' THEN
        IF NEW.object_key IS DISTINCT FROM OLD.object_key
            OR NEW.file_name IS DISTINCT FROM OLD.file_name
            OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes
            OR NEW.oci_digest IS DISTINCT FROM OLD.oci_digest
            OR NEW.status IS DISTINCT FROM OLD.status
            OR NEW.config_user IS DISTINCT FROM OLD.config_user
            OR NEW.config_entrypoint IS DISTINCT FROM OLD.config_entrypoint
            OR NEW.config_cmd IS DISTINCT FROM OLD.config_cmd
            OR NEW.config_env IS DISTINCT FROM OLD.config_env
            OR NEW.config_exposed_ports IS DISTINCT FROM OLD.config_exposed_ports
        THEN
            RAISE EXCEPTION 'only the description and default port of a built os image can be changed';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
