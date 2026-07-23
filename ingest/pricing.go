package ingest

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
)

type Pricing struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

var pricingMap map[string]Pricing

func init() {
	pricingMap = make(map[string]Pricing)
	LoadPricing()
}

func LoadPricing() {
	pwd, err := os.UserHomeDir()
	if err != nil {
		return
	}
	pricingFile := filepath.Join(pwd, ".config", "ai-tracker", "pricing.json")
	data, err := ioutil.ReadFile(pricingFile)
	if err == nil {
		json.Unmarshal(data, &pricingMap)
	}
}

func CalculateCost(model string, inTokens, outTokens float64) float64 {
	p, ok := pricingMap[model]
	if !ok {
		inCostPerM := 3.0
		outCostPerM := 10.0
		switch model {
		case "gemini-3.1-pro", "gemini-1.5-pro":
			inCostPerM, outCostPerM = 3.5, 10.5
		case "gemini-3.6-flash":
			inCostPerM, outCostPerM = 0.5, 1.5
		case "claude-5-sonnet", "claude-3.5-sonnet":
			inCostPerM, outCostPerM = 3.0, 15.0
		case "claude-4.8-opus":
			inCostPerM, outCostPerM = 15.0, 75.0
		case "fable-5", "sol-5.6", "terra-5.6":
			inCostPerM, outCostPerM = 2.0, 8.0
		}
		return (inTokens * inCostPerM / 1000000.0) + (outTokens * outCostPerM / 1000000.0)
	}
	return (inTokens * p.Input / 1000000.0) + (outTokens * p.Output / 1000000.0)
}
