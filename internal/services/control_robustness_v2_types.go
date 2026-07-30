package services

import (
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/domain"
)

const (
	ControlAnalysisFullSensitivity = "full_sensitivity"
	ControlAnalysisCommonSupport   = "common_support"

	ControlBalanceBalanced = "balanced"
	ControlBalanceWarning  = "warning"
	ControlBalanceFailed   = "failed"
)

type ControlRobustnessV2Config struct {
	CandidateStrideBlocks           uint64
	LiquidityActionExclusionBlocks  uint64
	BurnPlaceboSeparationBlocks     uint64
	ControlsPerBurn                 int
	MinimumControlsForCommonSupport int
	MaximumCandidateReuse           int
	TemporalPlaceboCaliper          float64
	MatchedBurnCaliper              float64
	PermutationIterations           int
	BootstrapIterations             int
	RandomSeed                      int64
}

func DefaultControlRobustnessV2Config(maximumHorizon uint64) ControlRobustnessV2Config {
	separation := maximumHorizon + 1
	if separation == 0 {
		separation = maximumHorizon
	}
	return ControlRobustnessV2Config{
		CandidateStrideBlocks:           1_500,
		LiquidityActionExclusionBlocks:  25,
		BurnPlaceboSeparationBlocks:     separation,
		ControlsPerBurn:                 3,
		MinimumControlsForCommonSupport: 2,
		MaximumCandidateReuse:           3,
		TemporalPlaceboCaliper:          1.25,
		MatchedBurnCaliper:              1.25,
		PermutationIterations:           20_000,
		BootstrapIterations:             20_000,
		RandomSeed:                      20_260_729,
	}
}

type ControlRobustnessV2Request struct {
	Collection          BurnSampleCollectionReport
	Realized            BurnRealizedDatasetReport
	Counterfactual      BurnCounterfactualFutureReport
	Horizons            []BurnOutcomeHorizon
	ZeroForOneAmountsIn []*big.Int
	OneForZeroAmountsIn []*big.Int
	ThresholdsBps       []decimal.Decimal
	AmountGridResolver  AnalysisAmountGridResolver
	Config              ControlRobustnessV2Config
}

type RobustTemporalPlaceboMatch struct {
	Match                  TemporalPlaceboMatch
	Rank                   int
	WithinCaliper          bool
	CandidateFinalUseCount int
}

type RobustTemporalPlaceboExclusion struct {
	BurnEventKey string
	BurnBlock    uint64
	Reason       string
	Detail       string
}

type RobustTemporalPlaceboAggregate struct {
	AnalysisSet                         string
	Burn                                domain.BurnCandidate
	BurnRangeLocation                   BurnRangeLocation
	BurnRangeActive                     bool
	HorizonLabel                        string
	HorizonBlocks                       uint64
	Included                            bool
	ExclusionReason                     string
	RequestedControls                   int
	SelectedControls                    int
	AvailableControls                   int
	WithinCaliperControls               int
	MeanMatchDistance                   float64
	MaximumMatchDistance                float64
	ImmediateTotalLSISBps               decimal.Decimal
	ActualRealizedDeteriorationBps      decimal.Decimal
	MeanPlaceboRealizedDeteriorationBps decimal.Decimal
	ExcessDeteriorationBps              decimal.Decimal
	ActualMechanicalMidpointEffectBps   decimal.Decimal
	ActualCounterfactualAvailable       bool
}

type RobustControlBalance struct {
	ControlType  string
	AnalysisSet  string
	Stratum      string
	Metric       string
	TreatedCount int
	ControlCount int
	TreatedMean  float64
	ControlMean  float64
	SMD          float64
	AbsoluteSMD  float64
	Status       string
}

type RobustControlInference struct {
	ControlType           string
	AnalysisSet           string
	Outcome               string
	Stratum               string
	HorizonLabel          string
	HorizonBlocks         uint64
	Pairs                 int
	MeanDifferenceBps     float64
	MedianDifferenceBps   float64
	PositivePairs         int
	PositiveShare         float64
	BootstrapMeanCILower  float64
	BootstrapMeanCIUpper  float64
	WilcoxonPValue        float64
	WilcoxonHolmPValue    float64
	PermutationPValue     float64
	PermutationHolmPValue float64
}

type RobustMatchedBurnPair struct {
	AnalysisSet   string
	PairID        string
	Stratum       BurnRangeLocation
	High          BurnEventSample
	Low           BurnEventSample
	MatchDistance float64
	BlockDistance uint64
	WithinCaliper bool
}

type RobustMatchedBurnOutcome struct {
	AnalysisSet                        string
	PairID                             string
	Stratum                            BurnRangeLocation
	HorizonLabel                       string
	HorizonBlocks                      uint64
	HighBurn                           domain.BurnCandidate
	LowBurn                            domain.BurnCandidate
	HighImmediateLSISBps               decimal.Decimal
	LowImmediateLSISBps                decimal.Decimal
	ImmediateLSISDifferenceBps         decimal.Decimal
	HighRealizedDeteriorationBps       decimal.Decimal
	LowRealizedDeteriorationBps        decimal.Decimal
	RealizedDeteriorationDifferenceBps decimal.Decimal
	MechanicalBothAvailable            bool
	MechanicalMidpointDifferenceBps    decimal.Decimal
}

type ControlRobustnessV2Manifest struct {
	Status                          string
	FailureDetail                   string
	PoolAddress                     string
	FromBlock                       uint64
	ToBlock                         uint64
	IndexedThrough                  uint64
	CandidateStrideBlocks           uint64
	CandidateBlocks                 int
	CandidateStates                 int
	CandidateStateSkips             int
	ControlsPerBurn                 int
	MinimumControlsForCommonSupport int
	MaximumCandidateReuse           int
	TemporalPlaceboCaliper          float64
	BurnPlaceboSeparationBlocks     uint64
	TemporalMatches                 int
	TemporalCommonSupportBurns      int
	TemporalExcludedBurns           int
	MatchedBurnCaliper              float64
	MatchedBurnFullPairs            int
	MatchedBurnCommonSupportPairs   int
	PermutationIterations           int
	BootstrapIterations             int
	RandomSeed                      int64
}

type ControlRobustnessV2Report struct {
	Manifest               ControlRobustnessV2Manifest
	TemporalMatches        []RobustTemporalPlaceboMatch
	TemporalCandidateSkips []TemporalPlaceboCandidateSkip
	TemporalExclusions     []RobustTemporalPlaceboExclusion
	TemporalObservations   []TemporalPlaceboObservation
	TemporalAggregates     []RobustTemporalPlaceboAggregate
	TemporalBalance        []RobustControlBalance
	TemporalInference      []RobustControlInference
	MatchedBurnPairs       []RobustMatchedBurnPair
	MatchedBurnOutcomes    []RobustMatchedBurnOutcome
	MatchedBurnBalance     []RobustControlBalance
	MatchedBurnInference   []RobustControlInference
}
