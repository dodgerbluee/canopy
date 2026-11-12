-- +goose Up
-- Human identities share the existing garden; owner_id remains the data boundary.
CREATE TABLE auth_users (
  id uuid PRIMARY KEY,
  owner_id uuid NOT NULL REFERENCES owner(id) ON DELETE CASCADE,
  username text NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9][a-z0-9_.-]{1,63}$'),
  display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 160),
  role text NOT NULL CHECK (role IN ('ADMIN','MEMBER')),
  disabled boolean NOT NULL DEFAULT false,
  is_primary boolean NOT NULL DEFAULT false,
  password_hash text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (id, owner_id)
);
CREATE UNIQUE INDEX auth_users_primary ON auth_users(owner_id) WHERE is_primary;
INSERT INTO auth_users(id,owner_id,username,display_name,role,is_primary)
SELECT id,id,CASE WHEN row_number() OVER (ORDER BY created_at,id)=1 THEN 'owner'
  ELSE 'owner-'||id::text END,'Garden owner','ADMIN',true FROM owner;

-- Preserve the operator CLI: new CLI-created owners receive a user automatically.
-- +goose StatementBegin
CREATE FUNCTION create_primary_auth_user() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO auth_users(id,owner_id,username,display_name,role,is_primary)
  VALUES(NEW.id,NEW.id,CASE WHEN EXISTS(SELECT 1 FROM auth_users WHERE username='owner')
    THEN 'owner-'||NEW.id::text ELSE 'owner' END,'Garden owner','ADMIN',true);
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER owner_auth_user AFTER INSERT ON owner
FOR EACH ROW EXECUTE FUNCTION create_primary_auth_user();

ALTER TABLE sessions ADD COLUMN user_id uuid;
ALTER TABLE sessions ADD CONSTRAINT sessions_user_garden_fk
  FOREIGN KEY (user_id,owner_id) REFERENCES auth_users(id,owner_id);
UPDATE sessions SET user_id=owner_id;
CREATE INDEX sessions_user_idx ON sessions(user_id);

CREATE TABLE oidc_connections (
  owner_id uuid PRIMARY KEY REFERENCES owner(id) ON DELETE CASCADE,
  display_name text NOT NULL,
  issuer text NOT NULL,
  client_id text NOT NULL,
  secret bytea NOT NULL,
  enabled boolean NOT NULL DEFAULT false,
  auto_join boolean NOT NULL DEFAULT false,
  revision integer NOT NULL DEFAULT 1
);
CREATE TABLE oidc_identities (
  issuer text NOT NULL,
  subject text NOT NULL,
  user_id uuid NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
  PRIMARY KEY (issuer,subject),
  UNIQUE(user_id,issuer)
);
CREATE TABLE oidc_attempts (
  state_hash text PRIMARY KEY,
  browser_hash text NOT NULL,
  owner_id uuid NOT NULL REFERENCES owner(id) ON DELETE CASCADE,
  user_id uuid REFERENCES auth_users(id) ON DELETE CASCADE,
  session_id uuid REFERENCES sessions(id) ON DELETE CASCADE,
  nonce text NOT NULL,
  verifier text NOT NULL,
  revision integer NOT NULL,
  expires_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE oidc_attempts,oidc_identities,oidc_connections;
ALTER TABLE sessions DROP CONSTRAINT sessions_user_garden_fk;
ALTER TABLE sessions DROP COLUMN user_id;
DROP TRIGGER owner_auth_user ON owner;
DROP FUNCTION create_primary_auth_user();
DROP TABLE auth_users;
