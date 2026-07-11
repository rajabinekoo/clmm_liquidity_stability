package utils

import "fmt"

const metaQuery = `
{
  _meta {
    block {
      number
      hash
    }
    hasIndexingErrors
  }
}`

func GenerateMetaQuery() string {
	return metaQuery
}

const poolMetadataQuery = `
{
  pool(id: "%s") {
    id
    feeTier
	feesUSD
    createdAtBlockNumber

	liquidity
    liquidityProviderCount
    sqrtPrice
    tick

    token0 {
      id
      decimals
    }

    token1 {
      id
      decimals
    }
  }
}`

func GeneratePoolMetadataQuery(pool string) string {
	return fmt.Sprintf(poolMetadataQuery, pool)
}

const poolSnapshotQuery = `
{
  pool(
    id: "%s"
    block: {number: %d}
  ) {
    id
    liquidity
    sqrtPrice
    tick
  }
}`

func GeneratePoolSnapshotQuery(
	pool string,
	blockNumber uint64,
) string {
	return fmt.Sprintf(
		poolSnapshotQuery,
		pool,
		blockNumber,
	)
}

const mintsQuery = `
{
  mints(
    first: %d
    orderBy: id
    orderDirection: asc
    where: {
      pool: "%s"
      id_gt: "%s"
      transaction_: {
        blockNumber_gte: %d
        blockNumber_lte: %d
      }
    }
  ) {
    id
    amount
    amount0
    amount1
    amountUSD

    logIndex
    timestamp

    origin
    owner
    sender

    tickLower
    tickUpper

    transaction {
      id
      blockNumber
    }
  }
}`

func GenerateMintsQuery(
	pool string,
	fromBlock uint64,
	toBlock uint64,
	afterID string,
	limit int,
) string {
	return fmt.Sprintf(
		mintsQuery,
		limit,
		pool,
		afterID,
		fromBlock,
		toBlock,
	)
}

const burnsQuery = `
{
  burns(
    first: %d
    orderBy: id
    orderDirection: asc
    where: {
      pool: "%s"
      id_gt: "%s"
      transaction_: {
        blockNumber_gte: %d
        blockNumber_lte: %d
      }
    }
  ) {
    id
    amount
    amount0
    amount1
    amountUSD

    logIndex
    timestamp

    origin
    owner

    tickLower
    tickUpper

    transaction {
      id
      blockNumber
    }
  }
}`

func GenerateBurnsQuery(
	pool string,
	fromBlock uint64,
	toBlock uint64,
	afterID string,
	limit int,
) string {
	return fmt.Sprintf(
		burnsQuery,
		limit,
		pool,
		afterID,
		fromBlock,
		toBlock,
	)
}
