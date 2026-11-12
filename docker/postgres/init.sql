-- Creates the api_app role, or sets its password if it exists. Pass the password as the api.app_password
-- setting, so it never lives in this file: PGOPTIONS="-c api.app_password=<PASSWORD>" psql -f init.sql
DO $$
DECLARE
  app_password text := current_setting('api.app_password', true);
BEGIN
  IF coalesce(app_password, '') = '' THEN
    RAISE EXCEPTION 'Set api.app_password, such as with PGOPTIONS="-c api.app_password=<PASSWORD>"';
  END IF;
  IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'api_app') THEN
    EXECUTE format('ALTER ROLE api_app LOGIN PASSWORD %L', app_password);
  ELSE
    EXECUTE format('CREATE ROLE api_app LOGIN PASSWORD %L', app_password);
  END IF;
END
$$;
GRANT USAGE ON SCHEMA public TO api_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO api_app;
