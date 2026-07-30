package controlfreeze

const (
	Version = "2.1"

	AnalysisPrimaryCommonSupport = "primary_common_support"
	AnalysisSensitivityCaliper2  = "sensitivity_caliper_2"
	AnalysisSensitivityFull      = "sensitivity_full_sample"

	RolePrimary      = "primary"
	RoleSensitivity  = "sensitivity"
	RoleExploratory  = "exploratory"
	RolePrimaryBlock = "primary_blocked"

	GatePassed        = "passed"
	GateFailed        = "failed"
	GateNotApplicable = "not_applicable"

	BalanceBalanced = "balanced"
	BalanceWarning  = "warning"
	BalanceFailed   = "failed"
)

type Config struct {
	TemporalPrimaryCaliper      float64
	TemporalSensitivityCaliper  float64
	MatchedPrimaryCaliper       float64
	MatchedSensitivityCaliper   float64
	MinimumTemporalControls     int
	MaximumAbsoluteSMD          float64
	Alpha                       float64
	BootstrapIterations         int
	PermutationIterations       int
	RandomSeed                  int64
	ExtremeTailPercentileCutoff float64
}

func DefaultConfig() Config {
	return Config{
		TemporalPrimaryCaliper:      1.25,
		TemporalSensitivityCaliper:  2.0,
		MatchedPrimaryCaliper:       1.25,
		MatchedSensitivityCaliper:   2.0,
		MinimumTemporalControls:     2,
		MaximumAbsoluteSMD:          0.20,
		Alpha:                       0.05,
		BootstrapIterations:         20_000,
		PermutationIterations:       20_000,
		RandomSeed:                  20_260_729,
		ExtremeTailPercentileCutoff: 0.90,
	}
}

type InputFiles struct {
	Manifest             string
	TemporalMatches      string
	TemporalObservations string
	MatchedPairs         string
	MatchedOutcomes      string
	Stem                 string
}

type OutputFiles struct {
	Manifest             string
	BalanceMetrics       string
	BalanceGate          string
	PublicationInference string
	AnalysisValues       string
	TemporalSupport      string
	MatchedSupport       string
}

type SourceManifest struct {
	Status                string
	PoolAddress           string
	FromBlock             uint64
	ToBlock               uint64
	IndexedThrough        uint64
	CandidateStates       int
	TemporalMatches       int
	MatchedBurnFullPairs  int
	BootstrapIterations   int
	PermutationIterations int
	RandomSeed            int64
}

type BalanceMetric struct {
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

type BalanceGate struct {
	ControlType        string
	AnalysisSet        string
	Stratum            string
	AnalysisRole       string
	MetricCount        int
	SampleSize         int
	MaximumAbsoluteSMD float64
	FailedMetricCount  int
	FailedMetrics      string
	Threshold          float64
	BalanceStatus      string
	GateStatus         string
	PrimaryEligible    bool
}

type Inference struct {
	ControlType            string
	AnalysisSet            string
	AnalysisRole           string
	Outcome                string
	Stratum                string
	HorizonLabel           string
	HorizonBlocks          uint64
	Pairs                  int
	MeanDifferenceBps      float64
	MedianDifferenceBps    float64
	PositivePairs          int
	PositiveShare          float64
	BootstrapMeanCILower   float64
	BootstrapMeanCIUpper   float64
	WilcoxonPValue         float64
	WilcoxonHolmPValue     float64
	PermutationPValue      float64
	PermutationHolmPValue  float64
	MaximumAbsoluteSMD     float64
	BalanceGateStatus      string
	PrimaryEligible        bool
	CIExcludesZero         bool
	WilcoxonSignificant    bool
	PermutationSignificant bool
	Direction              string
	PublicationStatus      string
}

type AnalysisValue struct {
	ControlType     string
	AnalysisSet     string
	AnalysisRole    string
	Stratum         string
	RecordID        string
	HorizonLabel    string
	HorizonBlocks   uint64
	DifferenceBps   float64
	PrimaryEligible bool
}

type TemporalSupport struct {
	BurnEventKey                     string
	BurnBlock                        uint64
	BurnLogIndex                     int
	Stratum                          string
	BurnRangeActive                  bool
	ImmediateLSISBps                 float64
	LSISPercentileRank               float64
	SelectedControls                 int
	WithinPrimaryCaliperControls     int
	WithinSensitivityCaliperControls int
	MinimumControlsRequired          int
	MinimumMatchDistance             float64
	MeanMatchDistance                float64
	MaximumMatchDistance             float64
	PrimaryIncluded                  bool
	SupportStatus                    string
	ExtremeTailEvent                 bool
	ExclusionReason                  string
}

type MatchedSupport struct {
	PairID               string
	Stratum              string
	HighEventKey         string
	LowEventKey          string
	HighImmediateLSISBps float64
	LowImmediateLSISBps  float64
	MatchDistance        float64
	PrimaryIncluded      bool
	SensitivityIncluded  bool
	SupportStatus        string
}

type Manifest struct {
	Status                      string
	FailureDetail               string
	Version                     string
	PoolAddress                 string
	FromBlock                   uint64
	ToBlock                     uint64
	IndexedThrough              uint64
	TemporalPrimaryCaliper      float64
	TemporalSensitivityCaliper  float64
	MatchedPrimaryCaliper       float64
	MatchedSensitivityCaliper   float64
	MinimumTemporalControls     int
	MaximumAbsoluteSMD          float64
	Alpha                       float64
	BootstrapIterations         int
	PermutationIterations       int
	RandomSeed                  int64
	TemporalBurns               int
	TemporalPrimarySupportBurns int
	TemporalOutsideSupportBurns int
	TemporalExtremeTailBurns    int
	MatchedFullPairs            int
	MatchedPrimarySupportPairs  int
	MatchedSensitivityPairs     int
	PrimaryInferenceRows        int
	PrimaryEligibleRows         int
	PrimaryBlockedRows          int
	PrimaryRobustRows           int
	PrimarySupportedRows        int
	PrimaryInconclusiveRows     int
	BalanceGateRows             int
	BalanceGatePassed           int
	BalanceGateFailed           int
}

type Report struct {
	Manifest        Manifest
	BalanceMetrics  []BalanceMetric
	BalanceGates    []BalanceGate
	Inference       []Inference
	AnalysisValues  []AnalysisValue
	TemporalSupport []TemporalSupport
	MatchedSupport  []MatchedSupport
}
