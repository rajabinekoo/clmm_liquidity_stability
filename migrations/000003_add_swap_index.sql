-- +goose Up

CREATE TABLE swaps
(
    pool_address       CHAR(42)       NOT NULL
        REFERENCES pools (address),

    block_number       BIGINT         NOT NULL,
    log_index          INTEGER        NOT NULL,

    id                 TEXT           NOT NULL,
    tx_hash            CHAR(66)       NOT NULL,
    timestamp_unix     BIGINT         NOT NULL,

    amount0_raw        NUMERIC(78, 0) NOT NULL,
    amount1_raw        NUMERIC(78, 0) NOT NULL,
    sqrt_price_x96     NUMERIC(78, 0) NOT NULL,
    tick_after         INTEGER        NOT NULL,

    created_at         TIMESTAMPTZ    NOT NULL DEFAULT NOW(),

    PRIMARY KEY (pool_address, block_number, log_index),

    CONSTRAINT uq_swaps_pool_id
        UNIQUE (pool_address, id),

    CONSTRAINT chk_swaps_block_positive
        CHECK (block_number > 0),

    CONSTRAINT chk_swaps_log_index_non_negative
        CHECK (log_index >= 0),

    CONSTRAINT chk_swaps_timestamp_non_negative
        CHECK (timestamp_unix > 0),

    CONSTRAINT chk_swaps_sqrt_price_positive
        CHECK (sqrt_price_x96 > 0)
);

-- Ordered range scans are the hot path for pre-burn replay, realized-flow
-- controls and no-burn counterfactual replay.
CREATE INDEX idx_swaps_pool_block_log
    ON swaps (pool_address, block_number, log_index)
    INCLUDE (
        id,
        tx_hash,
        timestamp_unix,
        amount0_raw,
        amount1_raw,
        sqrt_price_x96,
        tick_after
    );

-- FetchSwapsPage keeps ID pagination compatibility with the existing service
-- interfaces while the analyzer is migrated from The Graph to PostgreSQL.
CREATE INDEX idx_swaps_pool_id
    ON swaps (pool_address, id)
    INCLUDE (
        block_number,
        log_index,
        tx_hash,
        timestamp_unix,
        amount0_raw,
        amount1_raw,
        sqrt_price_x96,
        tick_after
    );

CREATE TABLE swap_indexer_checkpoints
(
    pool_address         CHAR(42) PRIMARY KEY
        REFERENCES pools (address),

    first_indexed_block  BIGINT      NOT NULL,
    last_completed_block BIGINT      NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_swap_checkpoint_first_positive
        CHECK (first_indexed_block > 0),

    CONSTRAINT chk_swap_checkpoint_contiguous
        CHECK (last_completed_block >= first_indexed_block - 1)
);

-- +goose Down

DROP TABLE swap_indexer_checkpoints;
DROP TABLE swaps;
