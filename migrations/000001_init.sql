-- +goose Up

CREATE TABLE pools
(
    address         CHAR(42) PRIMARY KEY,

    token0_address  CHAR(42)    NOT NULL,
    token1_address  CHAR(42)    NOT NULL,

    token0_decimals SMALLINT    NOT NULL,
    token1_decimals SMALLINT    NOT NULL,

    fee_tier        INTEGER     NOT NULL,
    tick_spacing    INTEGER     NOT NULL,

    created_block   BIGINT      NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE lp_actions
(
    id              TEXT PRIMARY KEY,

    pool_address    CHAR(42) NOT NULL REFERENCES pools(address),
    action          SMALLINT NOT NULL,

    tx_hash         CHAR(66) NOT NULL,
    block_number    BIGINT NOT NULL,
    log_index       INTEGER NOT NULL,
    timestamp       TIMESTAMPTZ NOT NULL,

    owner           CHAR(42),
    sender          CHAR(42),
    origin          CHAR(42),

    tick_lower      INTEGER NOT NULL,
    tick_upper      INTEGER NOT NULL,

    liquidity_delta NUMERIC(78,0) NOT NULL,

    amount0         NUMERIC(78,18),
    amount1         NUMERIC(78,18),
    amount_usd      DOUBLE PRECISION,

    UNIQUE (pool_address, tx_hash, log_index)
);

-- These ALTER statements are redundant since the constraints are already defined in the CREATE TABLE
-- But keeping them won't cause issues
ALTER TABLE lp_actions
    ADD CONSTRAINT fk_lp_actions_pool
        FOREIGN KEY (pool_address)
            REFERENCES pools (address);

ALTER TABLE lp_actions
    ADD CONSTRAINT uq_lp_actions_event
        UNIQUE (pool_address, tx_hash, log_index);

ALTER TABLE lp_actions
    ALTER COLUMN timestamp
        TYPE TIMESTAMPTZ
        USING timestamp AT TIME ZONE 'UTC';

CREATE TABLE pool_snapshots
(
    pool_address     CHAR(42)       NOT NULL
        REFERENCES pools (address),

    block_number     BIGINT         NOT NULL,

    sqrt_price_x96   NUMERIC(78, 0) NOT NULL,
    tick             INTEGER,
    active_liquidity NUMERIC(78, 0) NOT NULL,

    created_at       TIMESTAMPTZ    NOT NULL DEFAULT NOW(),

    PRIMARY KEY (pool_address, block_number)
);

CREATE TABLE indexer_checkpoints
(
    pool_address         CHAR(42) PRIMARY KEY
        REFERENCES pools (address),

    last_completed_block BIGINT      NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_snapshots_pool_block
    ON pool_snapshots (pool_address, block_number DESC);

-- +goose Down

DROP TABLE indexer_checkpoints;
DROP TABLE pool_snapshots;
DROP TABLE lp_actions;
DROP TABLE pools;