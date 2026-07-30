package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"

	"github.com/shopspring/decimal"
	"golang.org/x/sync/singleflight"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
	"github.com/rajabinekoo/clmm-liquidity-stability/internal/uniswapv3"
)

const (
	AnalysisAmountGridModeRaw          = "raw"
	AnalysisAmountGridModeTargetImpact = "target_impact"

	AnalysisAmountGridStatusResolved                 = "resolved"
	AnalysisAmountGridStatusUnreachable              = "unreachable"
	AnalysisAmountGridStatusSearchLimit              = "search_limit"
	AnalysisAmountGridStatusSimulationError          = "simulation_error"
	AnalysisAmountGridStatusInvalidCounterpartAmount = "invalid_counterpart_amount"
)

var maxUint256 = new(big.Int).Sub(
	new(big.Int).Lsh(big.NewInt(1), 256),
	big.NewInt(1),
)

// AnalysisAmountGridResolver resolves the executable amount grid for one exact
// pool state. Implementations must be deterministic and safe for concurrent use.
type AnalysisAmountGridResolver interface {
	Resolve(
		ctx context.Context,
		pool *domain.ReconstructedPool,
	) (ResolvedAnalysisAmountGrid, error)

	Mode() string
}

// AnalysisAmountGridAuditSource exposes immutable diagnostics accumulated by a
// resolver. The analyzer uses it to export one auditable cross-pool grid file.
type AnalysisAmountGridAuditSource interface {
	AnalysisAmountGridResolver

	AuditRecords() []AnalysisAmountGridAuditRecord
	AuditSummary() AnalysisAmountGridAuditSummary
}

type AnalysisAmountGridSearchConfig struct {
	TargetImpactsBps []decimal.Decimal
	MaxExpansions    int
	MaxBisections    int

	// MaxOutputQuantizationBps bounds the worst-case relative distortion caused
	// by rounding the simulated output to one raw token unit. A value of 0.1
	// requires at least 100,000 raw output units because 10,000 / 100,000 = 0.1
	// bps. This prevents tiny one-unit outputs from masquerading as market impact.
	MaxOutputQuantizationBps decimal.Decimal
}

type ResolvedAnalysisAmountGrid struct {
	StateID string
	Mode    string

	TargetImpactsBps []decimal.Decimal

	ZeroForOne []AnalysisAmountGridPoint
	OneForZero []AnalysisAmountGridPoint
}

type AnalysisAmountGridPoint struct {
	TargetImpactBps decimal.Decimal

	AmountInRaw  *big.Int
	AmountOutRaw *big.Int

	OutputQuantizationBoundBps decimal.Decimal

	AchievedImpactBps decimal.Decimal
	OvershootBps      decimal.Decimal

	Available bool
	Status    string
	Detail    string

	ExpansionSteps int
	BisectionSteps int
	SwapSteps      int
	CrossedTicks   int
}

type AnalysisAmountGridAuditRecord struct {
	StateID string
	Mode    string

	PoolAddress     string
	BlockNumber     uint64
	CurrentTick     int
	SqrtPriceX96    *big.Int
	ActiveLiquidity *big.Int

	ZeroForOne bool
	PointIndex int

	Point AnalysisAmountGridPoint
}

type AnalysisAmountGridAuditSummary struct {
	Mode string

	MaxOutputQuantizationBps decimal.Decimal
	MinimumOutputRaw         *big.Int

	ResolveRequests  int
	CacheHits        int
	UniqueStates     int
	CompleteStates   int
	IncompleteStates int

	ResolvedPoints    int
	UnavailablePoints int
}

type analysisAmountGridResolver struct {
	mode string

	simulator *uniswapv3.Simulator

	rawToken0Amounts []*big.Int
	search           AnalysisAmountGridSearchConfig
	minimumOutputRaw *big.Int

	mu sync.Mutex

	cache   map[string]ResolvedAnalysisAmountGrid
	records map[string][]AnalysisAmountGridAuditRecord

	resolveRequests int
	cacheHits       int

	group singleflight.Group
}

func NewTargetImpactAmountGridResolver(
	simulator *uniswapv3.Simulator,
	config AnalysisAmountGridSearchConfig,
) (AnalysisAmountGridAuditSource, error) {
	if simulator == nil {
		return nil, fmt.Errorf("target-impact amount grid: simulator is nil")
	}

	normalized, err := normalizeTargetImpactGridSearchConfig(config)
	if err != nil {
		return nil, err
	}

	minimumOutputRaw, err := minimumAnalysisGridOutputRaw(
		normalized.MaxOutputQuantizationBps,
	)
	if err != nil {
		return nil, err
	}

	return &analysisAmountGridResolver{
		mode:             AnalysisAmountGridModeTargetImpact,
		simulator:        simulator,
		search:           normalized,
		minimumOutputRaw: minimumOutputRaw,
		cache:            make(map[string]ResolvedAnalysisAmountGrid),
		records:          make(map[string][]AnalysisAmountGridAuditRecord),
	}, nil
}

func NewRawAnalysisAmountGridResolver(
	simulator *uniswapv3.Simulator,
	token0Amounts []*big.Int,
) (AnalysisAmountGridAuditSource, error) {
	if simulator == nil {
		return nil, fmt.Errorf("raw amount grid: simulator is nil")
	}

	amounts, err := normalizeAnalysisRawAmounts(token0Amounts)
	if err != nil {
		return nil, err
	}

	return &analysisAmountGridResolver{
		mode:             AnalysisAmountGridModeRaw,
		simulator:        simulator,
		rawToken0Amounts: amounts,
		cache:            make(map[string]ResolvedAnalysisAmountGrid),
		records:          make(map[string][]AnalysisAmountGridAuditRecord),
	}, nil
}

func (r *analysisAmountGridResolver) Mode() string {
	if r == nil {
		return ""
	}

	return r.mode
}

func (r *analysisAmountGridResolver) Resolve(
	ctx context.Context,
	pool *domain.ReconstructedPool,
) (ResolvedAnalysisAmountGrid, error) {
	if r == nil || r.simulator == nil {
		return ResolvedAnalysisAmountGrid{}, fmt.Errorf(
			"resolve analysis amount grid: resolver is incomplete",
		)
	}
	if err := validateAnalysisGridPool(pool); err != nil {
		return ResolvedAnalysisAmountGrid{}, err
	}
	if err := ctx.Err(); err != nil {
		return ResolvedAnalysisAmountGrid{}, err
	}

	stateID, err := analysisAmountGridStateID(pool)
	if err != nil {
		return ResolvedAnalysisAmountGrid{}, fmt.Errorf(
			"resolve analysis amount grid: fingerprint state: %w",
			err,
		)
	}

	r.mu.Lock()
	r.resolveRequests++
	if cached, exists := r.cache[stateID]; exists {
		r.cacheHits++
		r.mu.Unlock()

		return cloneResolvedAnalysisAmountGrid(cached), nil
	}
	r.mu.Unlock()

	value, err, _ := r.group.Do(stateID, func() (any, error) {
		r.mu.Lock()
		if cached, exists := r.cache[stateID]; exists {
			r.cacheHits++
			r.mu.Unlock()

			return cloneResolvedAnalysisAmountGrid(cached), nil
		}
		r.mu.Unlock()

		grid, records, buildErr := r.build(ctx, stateID, pool)
		if buildErr != nil {
			return ResolvedAnalysisAmountGrid{}, buildErr
		}

		r.mu.Lock()
		r.cache[stateID] = cloneResolvedAnalysisAmountGrid(grid)
		r.records[stateID] = cloneAnalysisAmountGridAuditRecords(records)
		r.mu.Unlock()

		return cloneResolvedAnalysisAmountGrid(grid), nil
	})
	if err != nil {
		return ResolvedAnalysisAmountGrid{}, err
	}

	grid, ok := value.(ResolvedAnalysisAmountGrid)
	if !ok {
		return ResolvedAnalysisAmountGrid{}, fmt.Errorf(
			"resolve analysis amount grid: unexpected cache value %T",
			value,
		)
	}

	return cloneResolvedAnalysisAmountGrid(grid), nil
}

func (r *analysisAmountGridResolver) build(
	ctx context.Context,
	stateID string,
	pool *domain.ReconstructedPool,
) (
	ResolvedAnalysisAmountGrid,
	[]AnalysisAmountGridAuditRecord,
	error,
) {
	switch r.mode {
	case AnalysisAmountGridModeTargetImpact:
		return r.buildTargetImpactGrid(ctx, stateID, pool)
	case AnalysisAmountGridModeRaw:
		return r.buildRawGrid(ctx, stateID, pool)
	default:
		return ResolvedAnalysisAmountGrid{}, nil, fmt.Errorf(
			"resolve analysis amount grid: unsupported mode %q",
			r.mode,
		)
	}
}

func (r *analysisAmountGridResolver) buildTargetImpactGrid(
	ctx context.Context,
	stateID string,
	pool *domain.ReconstructedPool,
) (
	ResolvedAnalysisAmountGrid,
	[]AnalysisAmountGridAuditRecord,
	error,
) {
	zeroForOne := r.resolveTargetImpactDirection(
		ctx,
		pool,
		true,
		r.search.TargetImpactsBps,
	)
	oneForZero := r.resolveTargetImpactDirection(
		ctx,
		pool,
		false,
		r.search.TargetImpactsBps,
	)

	grid := ResolvedAnalysisAmountGrid{
		StateID: stateID,
		Mode:    r.mode,
		TargetImpactsBps: append(
			[]decimal.Decimal(nil),
			r.search.TargetImpactsBps...,
		),
		ZeroForOne: zeroForOne,
		OneForZero: oneForZero,
	}

	records := analysisAmountGridRecordsForState(pool, grid)

	return grid, records, nil
}

func (r *analysisAmountGridResolver) resolveTargetImpactDirection(
	ctx context.Context,
	pool *domain.ReconstructedPool,
	zeroForOne bool,
	targets []decimal.Decimal,
) []AnalysisAmountGridPoint {
	result := make([]AnalysisAmountGridPoint, 0, len(targets))

	lowerAmount := big.NewInt(0)
	lowerImpact := decimal.Zero

	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			result = append(result, AnalysisAmountGridPoint{
				TargetImpactBps: target,
				Status:          AnalysisAmountGridStatusSimulationError,
				Detail:          err.Error(),
			})
			continue
		}

		point := r.searchTargetImpactAmount(
			ctx,
			pool,
			zeroForOne,
			target,
			lowerAmount,
			lowerImpact,
		)
		result = append(result, point)

		if !point.Available {
			// Higher targets cannot be resolved after an actual liquidity bound or
			// a deterministic search failure. Preserve one explicit row per target.
			for len(result) < len(targets) {
				nextTarget := targets[len(result)]
				result = append(result, AnalysisAmountGridPoint{
					TargetImpactBps: nextTarget,
					Status:          point.Status,
					Detail: fmt.Sprintf(
						"not searched because lower target %s was unavailable: %s",
						target,
						point.Detail,
					),
				})
			}
			break
		}

		lowerAmount = new(big.Int).Set(point.AmountInRaw)
		lowerImpact = point.AchievedImpactBps
	}

	return result
}

func (r *analysisAmountGridResolver) searchTargetImpactAmount(
	ctx context.Context,
	pool *domain.ReconstructedPool,
	zeroForOne bool,
	target decimal.Decimal,
	lowerAmount *big.Int,
	lowerImpact decimal.Decimal,
) AnalysisAmountGridPoint {
	point := AnalysisAmountGridPoint{
		TargetImpactBps: target,
		Status:          AnalysisAmountGridStatusSearchLimit,
	}

	if lowerAmount == nil || lowerAmount.Sign() < 0 {
		point.Status = AnalysisAmountGridStatusSimulationError
		point.Detail = "invalid lower search amount"
		return point
	}

	low := new(big.Int).Set(lowerAmount)
	lowImpact := lowerImpact

	// A price jump can satisfy more than one requested target at the amount
	// selected for the previous target. Curves still require strictly increasing
	// amounts, so the next exact raw unit is the mathematically minimal candidate.
	if low.Sign() > 0 && !lowImpact.LessThan(target) {
		candidate := new(big.Int).Add(low, big.NewInt(1))
		if candidate.Cmp(maxUint256) > 0 {
			point.Detail = "previous target already reached uint256 maximum"
			return point
		}

		result, err := r.simulateGridPoint(ctx, pool, zeroForOne, candidate)
		point.ExpansionSteps = 1
		if err != nil {
			point.Status = analysisGridSimulationFailureStatus(err)
			point.Detail = err.Error()
			return point
		}
		if !r.gridPointMeetsOutputPrecision(result) ||
			result.PriceImpactBps.LessThan(target) {
			// Output below the precision floor is not an executable normalized-grid
			// point even when integer rounding reports a large apparent impact.
			low = candidate
			lowImpact = result.PriceImpactBps
		} else {
			return resolvedAnalysisAmountGridPoint(
				target,
				candidate,
				result,
				1,
				0,
			)
		}
	}

	high := nextAnalysisGridSearchAmount(low)
	if high == nil {
		point.Detail = "cannot expand search beyond uint256 maximum"
		return point
	}

	var (
		candidateAmount *big.Int
		candidateResult *uniswapv3.ExactInputResult
		upperBoundary   *big.Int
	)

	for expansion := 1; expansion <= r.search.MaxExpansions; expansion++ {
		if err := ctx.Err(); err != nil {
			point.Status = AnalysisAmountGridStatusSimulationError
			point.Detail = err.Error()
			point.ExpansionSteps = expansion - 1
			return point
		}

		result, err := r.simulateGridPoint(ctx, pool, zeroForOne, high)
		point.ExpansionSteps = expansion
		if err != nil {
			if errors.Is(err, uniswapv3.ErrInsufficientLiquidity) {
				upperBoundary = new(big.Int).Set(high)
				break
			}

			point.Status = AnalysisAmountGridStatusSimulationError
			point.Detail = err.Error()
			return point
		}

		if r.gridPointMeetsOutputPrecision(result) &&
			!result.PriceImpactBps.LessThan(target) {
			candidateAmount = new(big.Int).Set(high)
			candidateResult = result
			upperBoundary = new(big.Int).Set(high)
			break
		}

		low = new(big.Int).Set(high)
		lowImpact = result.PriceImpactBps

		next := nextAnalysisGridSearchAmount(high)
		if next == nil {
			point.Detail = fmt.Sprintf(
				"target %s bps was not reached before uint256 maximum; last impact=%s",
				target,
				lowImpact,
			)
			return point
		}
		high = next
	}

	if upperBoundary == nil {
		point.Detail = fmt.Sprintf(
			"target %s bps was not bracketed after %d expansion steps; last impact=%s",
			target,
			r.search.MaxExpansions,
			lowImpact,
		)
		return point
	}

	upper := new(big.Int).Set(upperBoundary)
	one := big.NewInt(1)

	for bisection := 1; bisection <= r.search.MaxBisections; bisection++ {
		gap := new(big.Int).Sub(upper, low)
		if gap.Cmp(one) <= 0 {
			point.BisectionSteps = bisection - 1
			break
		}

		mid := new(big.Int).Add(low, new(big.Int).Rsh(gap, 1))
		if mid.Cmp(low) <= 0 || mid.Cmp(upper) >= 0 {
			point.BisectionSteps = bisection - 1
			break
		}

		result, err := r.simulateGridPoint(ctx, pool, zeroForOne, mid)
		point.BisectionSteps = bisection
		if err != nil {
			if errors.Is(err, uniswapv3.ErrInsufficientLiquidity) {
				upper = mid
				continue
			}

			point.Status = AnalysisAmountGridStatusSimulationError
			point.Detail = err.Error()
			return point
		}

		if r.gridPointMeetsOutputPrecision(result) &&
			!result.PriceImpactBps.LessThan(target) {
			candidateAmount = new(big.Int).Set(mid)
			candidateResult = result
			upper = mid
			continue
		}

		low = mid
		lowImpact = result.PriceImpactBps
	}

	if candidateAmount == nil || candidateResult == nil {
		point.Status = AnalysisAmountGridStatusUnreachable
		point.Detail = fmt.Sprintf(
			"target %s bps is unreachable before the executable liquidity boundary; last impact=%s",
			target,
			lowImpact,
		)
		return point
	}

	return resolvedAnalysisAmountGridPoint(
		target,
		candidateAmount,
		candidateResult,
		point.ExpansionSteps,
		point.BisectionSteps,
	)
}

func (r *analysisAmountGridResolver) buildRawGrid(
	ctx context.Context,
	stateID string,
	pool *domain.ReconstructedPool,
) (
	ResolvedAnalysisAmountGrid,
	[]AnalysisAmountGridAuditRecord,
	error,
) {
	zeroForOne := make(
		[]AnalysisAmountGridPoint,
		0,
		len(r.rawToken0Amounts),
	)
	oneForZero := make(
		[]AnalysisAmountGridPoint,
		0,
		len(r.rawToken0Amounts),
	)

	for index, token0Amount := range r.rawToken0Amounts {
		zfResult, zfErr := r.simulateGridPoint(
			ctx,
			pool,
			true,
			token0Amount,
		)
		zeroForOne = append(
			zeroForOne,
			rawAnalysisAmountGridPoint(index, token0Amount, zfResult, zfErr),
		)

		token1Amount, quoteErr := uniswapv3.QuoteToken1ForToken0Raw(
			pool.SqrtPriceX96,
			token0Amount,
		)
		if quoteErr != nil {
			oneForZero = append(oneForZero, AnalysisAmountGridPoint{
				Status: AnalysisAmountGridStatusInvalidCounterpartAmount,
				Detail: quoteErr.Error(),
			})
			continue
		}

		ofResult, ofErr := r.simulateGridPoint(
			ctx,
			pool,
			false,
			token1Amount,
		)
		oneForZero = append(
			oneForZero,
			rawAnalysisAmountGridPoint(index, token1Amount, ofResult, ofErr),
		)
	}

	grid := ResolvedAnalysisAmountGrid{
		StateID:    stateID,
		Mode:       r.mode,
		ZeroForOne: zeroForOne,
		OneForZero: oneForZero,
	}

	return grid, analysisAmountGridRecordsForState(pool, grid), nil
}

func (r *analysisAmountGridResolver) simulateGridPoint(
	ctx context.Context,
	pool *domain.ReconstructedPool,
	zeroForOne bool,
	amount *big.Int,
) (*uniswapv3.ExactInputResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return r.simulator.SimulateExactInput(
		pool,
		uniswapv3.ExactInputRequest{
			AmountIn:        new(big.Int).Set(amount),
			ZeroForOne:      zeroForOne,
			AllowZeroOutput: true,
		},
	)
}

func (r *analysisAmountGridResolver) gridPointMeetsOutputPrecision(
	result *uniswapv3.ExactInputResult,
) bool {
	return result != nil &&
		result.AmountOut != nil &&
		r.minimumOutputRaw != nil &&
		result.AmountOut.Cmp(r.minimumOutputRaw) >= 0
}

func minimumAnalysisGridOutputRaw(
	maxQuantizationBps decimal.Decimal,
) (*big.Int, error) {
	if maxQuantizationBps.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf(
			"target-impact amount grid: max output quantization bps must be positive",
		)
	}

	minimum := decimal.NewFromInt(10_000).
		Div(maxQuantizationBps).
		Ceil().
		BigInt()
	if minimum.Sign() <= 0 {
		return nil, fmt.Errorf(
			"target-impact amount grid: derived minimum output is invalid",
		)
	}

	return minimum, nil
}

func outputQuantizationBoundBps(amountOut *big.Int) decimal.Decimal {
	if amountOut == nil || amountOut.Sign() <= 0 {
		return decimal.Zero
	}

	return decimal.NewFromInt(10_000).
		Div(decimal.NewFromBigInt(amountOut, 0))
}

func (r *analysisAmountGridResolver) AuditRecords() []AnalysisAmountGridAuditRecord {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	stateIDs := make([]string, 0, len(r.records))
	for stateID := range r.records {
		stateIDs = append(stateIDs, stateID)
	}
	sort.Strings(stateIDs)

	result := make([]AnalysisAmountGridAuditRecord, 0)
	for _, stateID := range stateIDs {
		result = append(
			result,
			cloneAnalysisAmountGridAuditRecords(r.records[stateID])...,
		)
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].BlockNumber != result[j].BlockNumber {
			return result[i].BlockNumber < result[j].BlockNumber
		}
		if result[i].StateID != result[j].StateID {
			return result[i].StateID < result[j].StateID
		}
		if result[i].ZeroForOne != result[j].ZeroForOne {
			return result[i].ZeroForOne
		}
		return result[i].PointIndex < result[j].PointIndex
	})

	return result
}

func (r *analysisAmountGridResolver) AuditSummary() AnalysisAmountGridAuditSummary {
	if r == nil {
		return AnalysisAmountGridAuditSummary{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	summary := AnalysisAmountGridAuditSummary{
		Mode:            r.mode,
		ResolveRequests: r.resolveRequests,
		CacheHits:       r.cacheHits,
		UniqueStates:    len(r.cache),
	}
	if r.mode == AnalysisAmountGridModeTargetImpact {
		summary.MaxOutputQuantizationBps = r.search.MaxOutputQuantizationBps
		if r.minimumOutputRaw != nil {
			summary.MinimumOutputRaw = new(big.Int).Set(r.minimumOutputRaw)
		}
	}

	for _, grid := range r.cache {
		if grid.Complete() {
			summary.CompleteStates++
		} else {
			summary.IncompleteStates++
		}

		for _, point := range append(
			append([]AnalysisAmountGridPoint(nil), grid.ZeroForOne...),
			grid.OneForZero...,
		) {
			if point.Available {
				summary.ResolvedPoints++
			} else {
				summary.UnavailablePoints++
			}
		}
	}

	return summary
}

func (g ResolvedAnalysisAmountGrid) Complete() bool {
	if len(g.ZeroForOne) == 0 || len(g.OneForZero) == 0 {
		return false
	}
	if g.Mode == AnalysisAmountGridModeTargetImpact &&
		(len(g.ZeroForOne) != len(g.TargetImpactsBps) ||
			len(g.OneForZero) != len(g.TargetImpactsBps)) {
		return false
	}

	return analysisAmountGridDirectionComplete(g.ZeroForOne) &&
		analysisAmountGridDirectionComplete(g.OneForZero)
}

func (g ResolvedAnalysisAmountGrid) RequireComplete() error {
	if g.Complete() {
		return nil
	}

	return fmt.Errorf(
		"analysis amount grid state %s mode=%s is incomplete: zero_for_one=%s one_for_zero=%s",
		g.StateID,
		g.Mode,
		analysisAmountGridDirectionFailure(g.ZeroForOne),
		analysisAmountGridDirectionFailure(g.OneForZero),
	)
}

func (g ResolvedAnalysisAmountGrid) ZeroForOneAmountsIn() ([]*big.Int, error) {
	return analysisAmountGridAmounts(g.ZeroForOne)
}

func (g ResolvedAnalysisAmountGrid) OneForZeroAmountsIn() ([]*big.Int, error) {
	return analysisAmountGridAmounts(g.OneForZero)
}

func analysisAmountGridAmounts(points []AnalysisAmountGridPoint) ([]*big.Int, error) {
	if !analysisAmountGridDirectionComplete(points) {
		return nil, fmt.Errorf(
			"analysis amount grid direction is incomplete: %s",
			analysisAmountGridDirectionFailure(points),
		)
	}

	result := make([]*big.Int, len(points))
	for index, point := range points {
		result[index] = new(big.Int).Set(point.AmountInRaw)
	}

	return result, nil
}

func analysisAmountGridDirectionComplete(points []AnalysisAmountGridPoint) bool {
	if len(points) < 2 {
		return false
	}

	var previous *big.Int
	for _, point := range points {
		if !point.Available || point.AmountInRaw == nil || point.AmountInRaw.Sign() <= 0 {
			return false
		}
		if previous != nil && previous.Cmp(point.AmountInRaw) >= 0 {
			return false
		}
		previous = point.AmountInRaw
	}

	return true
}

func analysisAmountGridDirectionFailure(points []AnalysisAmountGridPoint) string {
	if len(points) == 0 {
		return "empty"
	}

	for index, point := range points {
		if !point.Available {
			return fmt.Sprintf(
				"point=%d target=%s status=%s detail=%s",
				index,
				point.TargetImpactBps,
				point.Status,
				point.Detail,
			)
		}
		if point.AmountInRaw == nil || point.AmountInRaw.Sign() <= 0 {
			return fmt.Sprintf("point=%d has invalid amount", index)
		}
		if index > 0 && points[index-1].AmountInRaw.Cmp(point.AmountInRaw) >= 0 {
			return fmt.Sprintf("amounts are not strictly increasing at point=%d", index)
		}
	}

	if len(points) < 2 {
		return "fewer than two points"
	}

	return "unknown"
}

func resolvedAnalysisAmountGridPoint(
	target decimal.Decimal,
	amount *big.Int,
	result *uniswapv3.ExactInputResult,
	expansions int,
	bisections int,
) AnalysisAmountGridPoint {
	return AnalysisAmountGridPoint{
		TargetImpactBps:            target,
		AmountInRaw:                new(big.Int).Set(amount),
		AmountOutRaw:               new(big.Int).Set(result.AmountOut),
		OutputQuantizationBoundBps: outputQuantizationBoundBps(result.AmountOut),
		AchievedImpactBps:          result.PriceImpactBps,
		OvershootBps:               result.PriceImpactBps.Sub(target),
		Available:                  true,
		Status:                     AnalysisAmountGridStatusResolved,
		ExpansionSteps:             expansions,
		BisectionSteps:             bisections,
		SwapSteps:                  result.SwapSteps,
		CrossedTicks:               result.CrossedTicks,
	}
}

func rawAnalysisAmountGridPoint(
	index int,
	amount *big.Int,
	result *uniswapv3.ExactInputResult,
	err error,
) AnalysisAmountGridPoint {
	point := AnalysisAmountGridPoint{
		AmountInRaw: new(big.Int).Set(amount),
	}
	if err != nil {
		point.Status = analysisGridSimulationFailureStatus(err)
		point.Detail = err.Error()
		return point
	}

	point.Available = true
	point.Status = AnalysisAmountGridStatusResolved
	point.AmountOutRaw = new(big.Int).Set(result.AmountOut)
	point.OutputQuantizationBoundBps = outputQuantizationBoundBps(result.AmountOut)
	point.AchievedImpactBps = result.PriceImpactBps
	point.SwapSteps = result.SwapSteps
	point.CrossedTicks = result.CrossedTicks
	point.Detail = fmt.Sprintf("raw_grid_index=%d", index)

	return point
}

func analysisGridSimulationFailureStatus(err error) string {
	if errors.Is(err, uniswapv3.ErrInsufficientLiquidity) {
		return AnalysisAmountGridStatusUnreachable
	}

	return AnalysisAmountGridStatusSimulationError
}

func nextAnalysisGridSearchAmount(current *big.Int) *big.Int {
	if current == nil || current.Sign() < 0 {
		return nil
	}

	if current.Sign() == 0 {
		return big.NewInt(1)
	}

	next := new(big.Int).Lsh(new(big.Int).Set(current), 1)
	if next.Cmp(current) <= 0 || next.Cmp(maxUint256) > 0 {
		if current.Cmp(maxUint256) >= 0 {
			return nil
		}
		return new(big.Int).Set(maxUint256)
	}

	return next
}

func normalizeTargetImpactGridSearchConfig(
	config AnalysisAmountGridSearchConfig,
) (AnalysisAmountGridSearchConfig, error) {
	if len(config.TargetImpactsBps) == 0 {
		return AnalysisAmountGridSearchConfig{}, fmt.Errorf(
			"target-impact amount grid: targets are empty",
		)
	}

	targets := append([]decimal.Decimal(nil), config.TargetImpactsBps...)
	for index, target := range targets {
		if target.LessThanOrEqual(decimal.Zero) {
			return AnalysisAmountGridSearchConfig{}, fmt.Errorf(
				"target-impact amount grid: target index %d must be positive",
				index,
			)
		}
		if index > 0 && !targets[index-1].LessThan(target) {
			return AnalysisAmountGridSearchConfig{}, fmt.Errorf(
				"target-impact amount grid: targets must be strictly increasing",
			)
		}
	}

	if config.MaxOutputQuantizationBps.LessThanOrEqual(decimal.Zero) {
		config.MaxOutputQuantizationBps = decimal.RequireFromString("0.1")
	}
	if config.MaxOutputQuantizationBps.GreaterThan(decimal.NewFromInt(1)) {
		return AnalysisAmountGridSearchConfig{}, fmt.Errorf(
			"target-impact amount grid: max output quantization bps %s exceeds 1",
			config.MaxOutputQuantizationBps,
		)
	}
	if _, err := minimumAnalysisGridOutputRaw(config.MaxOutputQuantizationBps); err != nil {
		return AnalysisAmountGridSearchConfig{}, err
	}

	if config.MaxExpansions <= 0 {
		config.MaxExpansions = 256
	}
	if config.MaxBisections <= 0 {
		config.MaxBisections = 256
	}
	if config.MaxExpansions > 512 {
		return AnalysisAmountGridSearchConfig{}, fmt.Errorf(
			"target-impact amount grid: max expansions %d exceeds 512",
			config.MaxExpansions,
		)
	}
	if config.MaxBisections > 512 {
		return AnalysisAmountGridSearchConfig{}, fmt.Errorf(
			"target-impact amount grid: max bisections %d exceeds 512",
			config.MaxBisections,
		)
	}

	config.TargetImpactsBps = targets

	return config, nil
}

func normalizeAnalysisRawAmounts(values []*big.Int) ([]*big.Int, error) {
	if len(values) < 2 {
		return nil, fmt.Errorf("raw amount grid: at least two amounts are required")
	}

	result := make([]*big.Int, len(values))
	for index, value := range values {
		if value == nil || value.Sign() <= 0 {
			return nil, fmt.Errorf("raw amount grid: amount index %d must be positive", index)
		}
		if value.BitLen() > 256 {
			return nil, fmt.Errorf("raw amount grid: amount index %d exceeds uint256", index)
		}
		if index > 0 && values[index-1].Cmp(value) >= 0 {
			return nil, fmt.Errorf("raw amount grid: amounts must be strictly increasing")
		}
		result[index] = new(big.Int).Set(value)
	}

	return result, nil
}

func validateAnalysisGridPool(pool *domain.ReconstructedPool) error {
	if pool == nil {
		return fmt.Errorf("resolve analysis amount grid: pool is nil")
	}
	if strings.TrimSpace(pool.PoolAddress) == "" {
		return fmt.Errorf("resolve analysis amount grid: pool address is required")
	}
	if pool.SqrtPriceX96 == nil || pool.SqrtPriceX96.Sign() <= 0 {
		return fmt.Errorf("resolve analysis amount grid: sqrt price must be positive")
	}
	if pool.Liquidity == nil || pool.Liquidity.Sign() <= 0 {
		return fmt.Errorf("resolve analysis amount grid: active liquidity must be positive")
	}

	return nil
}

func analysisAmountGridStateID(pool *domain.ReconstructedPool) (string, error) {
	if err := validateAnalysisGridPool(pool); err != nil {
		return "", err
	}

	hash := sha256.New()
	write := func(value string) {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}

	write(strings.ToLower(strings.TrimSpace(pool.PoolAddress)))
	write(fmt.Sprintf("%d", pool.BlockNumber))
	write(fmt.Sprintf("%d", pool.CurrentTick))
	write(pool.SqrtPriceX96.String())
	write(pool.Liquidity.String())

	ticks := append([]int(nil), pool.InitializedTicks...)
	sort.Ints(ticks)
	for _, index := range ticks {
		write(fmt.Sprintf("%d", index))
		tick := pool.Ticks[index]
		if tick == nil {
			write("nil")
			continue
		}
		if tick.LiquidityGross == nil {
			write("gross:nil")
		} else {
			write("gross:" + tick.LiquidityGross.String())
		}
		if tick.LiquidityNet == nil {
			write("net:nil")
		} else {
			write("net:" + tick.LiquidityNet.String())
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func analysisAmountGridRecordsForState(
	pool *domain.ReconstructedPool,
	grid ResolvedAnalysisAmountGrid,
) []AnalysisAmountGridAuditRecord {
	result := make(
		[]AnalysisAmountGridAuditRecord,
		0,
		len(grid.ZeroForOne)+len(grid.OneForZero),
	)

	appendDirection := func(zeroForOne bool, points []AnalysisAmountGridPoint) {
		for index, point := range points {
			result = append(result, AnalysisAmountGridAuditRecord{
				StateID:         grid.StateID,
				Mode:            grid.Mode,
				PoolAddress:     strings.ToLower(strings.TrimSpace(pool.PoolAddress)),
				BlockNumber:     pool.BlockNumber,
				CurrentTick:     pool.CurrentTick,
				SqrtPriceX96:    new(big.Int).Set(pool.SqrtPriceX96),
				ActiveLiquidity: new(big.Int).Set(pool.Liquidity),
				ZeroForOne:      zeroForOne,
				PointIndex:      index,
				Point:           cloneAnalysisAmountGridPoint(point),
			})
		}
	}

	appendDirection(true, grid.ZeroForOne)
	appendDirection(false, grid.OneForZero)

	return result
}

func cloneResolvedAnalysisAmountGrid(
	value ResolvedAnalysisAmountGrid,
) ResolvedAnalysisAmountGrid {
	return ResolvedAnalysisAmountGrid{
		StateID: value.StateID,
		Mode:    value.Mode,
		TargetImpactsBps: append(
			[]decimal.Decimal(nil),
			value.TargetImpactsBps...,
		),
		ZeroForOne: cloneAnalysisAmountGridPoints(value.ZeroForOne),
		OneForZero: cloneAnalysisAmountGridPoints(value.OneForZero),
	}
}

func cloneAnalysisAmountGridPoints(
	values []AnalysisAmountGridPoint,
) []AnalysisAmountGridPoint {
	result := make([]AnalysisAmountGridPoint, len(values))
	for index, value := range values {
		result[index] = cloneAnalysisAmountGridPoint(value)
	}

	return result
}

func cloneAnalysisAmountGridPoint(
	value AnalysisAmountGridPoint,
) AnalysisAmountGridPoint {
	result := value
	if value.AmountInRaw != nil {
		result.AmountInRaw = new(big.Int).Set(value.AmountInRaw)
	}
	if value.AmountOutRaw != nil {
		result.AmountOutRaw = new(big.Int).Set(value.AmountOutRaw)
	}

	return result
}

func cloneAnalysisAmountGridAuditRecords(
	values []AnalysisAmountGridAuditRecord,
) []AnalysisAmountGridAuditRecord {
	result := make([]AnalysisAmountGridAuditRecord, len(values))
	for index, value := range values {
		result[index] = value
		if value.SqrtPriceX96 != nil {
			result[index].SqrtPriceX96 = new(big.Int).Set(value.SqrtPriceX96)
		}
		if value.ActiveLiquidity != nil {
			result[index].ActiveLiquidity = new(big.Int).Set(value.ActiveLiquidity)
		}
		result[index].Point = cloneAnalysisAmountGridPoint(value.Point)
	}

	return result
}
