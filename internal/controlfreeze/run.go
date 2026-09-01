package controlfreeze

import "fmt"

func Run(inputDirectory string, config Config) (OutputFiles, Report, error) {
	inputs, err := DiscoverInputFiles(inputDirectory)
	if err != nil {
		return OutputFiles{}, Report{}, err
	}
	outputs := OutputFilesFor(inputs)
	parsed, err := loadInputs(inputs)
	if err != nil {
		failure := Manifest{Status: "failed", FailureDetail: err.Error(), Version: Version}
		_ = writeFailureManifest(outputs.Manifest, failure)
		return outputs, Report{}, err
	}
	report, err := Analyze(parsed, config)
	if err != nil {
		failure := Manifest{
			Status: "failed", FailureDetail: err.Error(), Version: Version,
			PoolAddress: parsed.Manifest.PoolAddress, FromBlock: parsed.Manifest.FromBlock,
			ToBlock: parsed.Manifest.ToBlock, IndexedThrough: parsed.Manifest.IndexedThrough,
		}
		_ = writeFailureManifest(outputs.Manifest, failure)
		return outputs, Report{}, err
	}
	if err := WriteReport(outputs, report); err != nil {
		failure := report.Manifest
		failure.Status = "failed"
		failure.FailureDetail = err.Error()
		_ = writeFailureManifest(outputs.Manifest, failure)
		return outputs, Report{}, fmt.Errorf("control freeze v2.1: %w", err)
	}
	return outputs, report, nil
}
