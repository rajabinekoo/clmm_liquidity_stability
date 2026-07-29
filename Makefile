# -------------------------
# CLMM LSIS research tools
# by: Ali Rajabi Nekoo
# -------------------------

# Connection settings
DB_HOST ?= localhost
DB_PORT ?= 5432
DB_USER ?= postgres
DB_PASSWORD ?= postgres
DB_NAME ?= clmm_liquidity_stability_db
DB_SSLMODE ?= disable

# Build connection string
DB_CONNECTION = host=$(DB_HOST) port=$(DB_PORT) user=$(DB_USER) password=$(DB_PASSWORD) dbname=$(DB_NAME) sslmode=$(DB_SSLMODE)

# Migration directory
MIGRATION_DIR = ./migrations

# Container infra (docker-compose service names live in deployments/docker-compose.yml).
COMPOSE_FILE   := docker-compose.yml
COMPOSE        ?= docker compose -f $(COMPOSE_FILE)

# App commands
INDEXER_CMD := go run ./cmd/indexer/main.go
ANALYZER_CMD := go run ./cmd/analyzer

.PHONY: infra-up
infra-up:
	$(COMPOSE) up -d

.PHONY: infra-down
infra-down:
	$(COMPOSE) down

.PHONY: index-usdc-weth-005
index-usdc-weth-005:
	@ENV_FILE=.env.usdc_weth_005 $(INDEXER_CMD)

.PHONY: index-usdc-weth-030
index-usdc-weth-030:
	@ENV_FILE=.env.usdc_weth_030 $(INDEXER_CMD)

.PHONY: index-wbtc-weth-030
index-wbtc-weth-030:
	@ENV_FILE=.env.wbtc_weth_030 $(INDEXER_CMD)

.PHONY: index-weth-usdt-005
index-weth-usdt-005:
	@ENV_FILE=.env.weth_usdt_005 $(INDEXER_CMD)

.PHONY: index-usdc-usdt-001
index-usdc-usdt-001:
	@ENV_FILE=.env.usdc_usdt_001 $(INDEXER_CMD)

.PHONY: index-pools
index-pools: index-usdc-weth-005 index-usdc-weth-030 index-wbtc-weth-030 index-weth-usdt-005 index-usdc-usdt-001

# One-shot local Swap backfills. LP actions/snapshots are left untouched; the
# command exits as soon as the PostgreSQL Swap checkpoint reaches the safe head.
.PHONY: backfill-swaps-usdc-weth-005
backfill-swaps-usdc-weth-005:
	@ENV_FILE=.env.usdc_weth_005 INDEXER_ONCE=true INDEX_LP_ACTIONS=false INDEX_SWAPS=true $(INDEXER_CMD)

.PHONY: backfill-swaps-usdc-weth-030
backfill-swaps-usdc-weth-030:
	@ENV_FILE=.env.usdc_weth_030 INDEXER_ONCE=true INDEX_LP_ACTIONS=false INDEX_SWAPS=true $(INDEXER_CMD)

.PHONY: backfill-swaps-wbtc-weth-030
backfill-swaps-wbtc-weth-030:
	@ENV_FILE=.env.wbtc_weth_030 INDEXER_ONCE=true INDEX_LP_ACTIONS=false INDEX_SWAPS=true $(INDEXER_CMD)

.PHONY: backfill-swaps-weth-usdt-005
backfill-swaps-weth-usdt-005:
	@ENV_FILE=.env.weth_usdt_005 INDEXER_ONCE=true INDEX_LP_ACTIONS=false INDEX_SWAPS=true $(INDEXER_CMD)

.PHONY: backfill-swaps-usdc-usdt-001
backfill-swaps-usdc-usdt-001:
	@ENV_FILE=.env.usdc_usdt_001 INDEXER_ONCE=true INDEX_LP_ACTIONS=false INDEX_SWAPS=true $(INDEXER_CMD)

.PHONY: backfill-swaps
backfill-swaps: backfill-swaps-usdc-weth-005 backfill-swaps-usdc-weth-030 backfill-swaps-wbtc-weth-030 backfill-swaps-weth-usdt-005 backfill-swaps-usdc-usdt-001

.PHONY: analyze-usdc-weth-005
analyze-usdc-weth-005:
	@ENV_FILE=.env.usdc_weth_005 $(ANALYZER_CMD)

.PHONY: analyze-usdc-weth-030
analyze-usdc-weth-030:
	@ENV_FILE=.env.usdc_weth_030 $(ANALYZER_CMD)

.PHONY: analyze-wbtc-weth-030
analyze-wbtc-weth-030:
	@ENV_FILE=.env.wbtc_weth_030 $(ANALYZER_CMD)

.PHONY: analyze-weth-usdt-005
analyze-weth-usdt-005:
	@ENV_FILE=.env.weth_usdt_005 $(ANALYZER_CMD)

.PHONY: analyze-usdc-usdt-001
analyze-usdc-usdt-001:
	@ENV_FILE=.env.usdc_usdt_001 $(ANALYZER_CMD)

.PHONY: analyze-pools
analyze-pools: analyze-usdc-weth-005 analyze-usdc-weth-030 analyze-wbtc-weth-030 analyze-weth-usdt-005 analyze-usdc-usdt-001

.PHONY: research-pipeline
research-pipeline: index-pools analyze-pools

.PHONY: goose-up
goose-up:
	goose postgres "$(DB_CONNECTION)" -dir $(MIGRATION_DIR) up

.PHONY: goose-down
goose-down:
	goose postgres "$(DB_CONNECTION)" -dir $(MIGRATION_DIR) down

.PHONY: goose-reset
goose-reset:
	goose postgres "$(DB_CONNECTION)" -dir $(MIGRATION_DIR) reset

.PHONY: goose-create
goose-create:
	@read -p "Enter migration name: " name; \
	goose -dir $(MIGRATION_DIR) create $$name sql

.PHONY: goose-status
goose-status:
	goose postgres "$(DB_CONNECTION)" -dir $(MIGRATION_DIR) status