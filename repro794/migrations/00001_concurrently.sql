-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY idx_users_email ON users (email);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_users_email;
