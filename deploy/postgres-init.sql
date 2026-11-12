-- Development-only bootstrap script. It runs once when the Postgres volume is
-- first created. Migrations run as the `waypoint` superuser; the API connects
-- as `waypoint_app`, which is deliberately NOT a superuser and NOT BYPASSRLS.
CREATE ROLE waypoint_app
    LOGIN PASSWORD 'waypoint_app'
    NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;

GRANT CONNECT ON DATABASE waypoint TO waypoint_app;
GRANT USAGE ON SCHEMA public TO waypoint_app;

ALTER DEFAULT PRIVILEGES FOR ROLE waypoint IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO waypoint_app;

ALTER DEFAULT PRIVILEGES FOR ROLE waypoint IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO waypoint_app;
