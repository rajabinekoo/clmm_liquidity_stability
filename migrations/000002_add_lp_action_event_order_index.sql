-- +goose Up

CREATE INDEX IF NOT EXISTS idx_lp_actions_pool_block_log
    ON lp_actions (
                   pool_address,
                   block_number,
                   log_index
        );

-- +goose Down

DROP INDEX IF EXISTS idx_lp_actions_pool_block_log;