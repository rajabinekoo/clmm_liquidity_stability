package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/rajabinekoo/clmm-liquidity-stability/internal/controlfreeze"
)

func main() {
	inputDirectory := flag.String("input-dir", "outputs/usdc_weth_005", "directory containing control v2 CSV outputs")
	flag.Parse()

	config := controlfreeze.DefaultConfig()
	outputs, report, err := controlfreeze.Run(*inputDirectory, config)
	if err != nil {
		log.Printf("control freeze v2.1 failed: %v", err)
		if outputs.Manifest != "" {
			log.Printf("failure manifest: %s", outputs.Manifest)
		}
		os.Exit(1)
	}
	fmt.Println(controlfreeze.OutputSummary(outputs, report))
}
