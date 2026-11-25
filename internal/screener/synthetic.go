package screener

import (
	"errors"
	"strings"
)

const SyntheticCategory = "Synthetic Instruments"
const SyntheticSimulationBasis = "Synthetic ratio observations; returns are analytical and do not represent an executable position."

// IDs are canonical downloader identities. NativeID is the matching JForex SDK
// identifier when the provider publishes this instrument directly.
type SyntheticDefinition struct {
	ID, Name, Numerator, Denominator, NativeID, EarliestDate, NativeHistoryFrom, NativeHistoryFromTime string
	Calculated                                                                                         bool
}

var syntheticDefinitions = []SyntheticDefinition{
	{ID: "btcusd", Name: "Bitcoin / US Dollar", Numerator: "btcusd", NativeID: "BTCUSD", EarliestDate: "2000-01-01", NativeHistoryFrom: "2017-05-07", NativeHistoryFromTime: "2017-05-07T23:59:59.058Z"},
	{ID: "btcxau", Name: "Bitcoin / Gold", Numerator: "btcusd", Denominator: "xauusd", EarliestDate: "2017-05-07", Calculated: true},
	{ID: "xauspx", Name: "Gold / S&P 500 Index", Numerator: "xauusd", Denominator: "usa500idxusd", EarliestDate: "2012-01-16", Calculated: true},
	{ID: "ethbtc", Name: "Ethereum / Bitcoin", Numerator: "ethusd", Denominator: "btcusd", EarliestDate: "2017-12-11", Calculated: true},
	{ID: "xauxag", Name: "Gold / Silver", Numerator: "xauusd", Denominator: "xagusd", NativeID: "XAUXAG", EarliestDate: "2003-05-05", NativeHistoryFrom: "2024-09-17", NativeHistoryFromTime: "2024-09-17T09:39:43.256Z"},
}

func SyntheticDefinitions() []SyntheticDefinition {
	return append([]SyntheticDefinition(nil), syntheticDefinitions...)
}

func SyntheticDefinitionFor(id string) (SyntheticDefinition, bool) {
	for _, definition := range syntheticDefinitions {
		if strings.EqualFold(id, definition.ID) {
			return definition, true
		}
	}
	return SyntheticDefinition{}, false
}

func calculatedSynthetic(symbol string) bool {
	definition, found := SyntheticDefinitionFor(symbol)
	return found && definition.Calculated
}

// DataProvenance accompanies the actual published dataset, including a native
// XAUXAG dataset versus a calculated fallback under its same canonical identity.
type DataProvenance struct {
	Version               int                 `json:"version"`
	Kind                  string              `json:"kind"`
	Numerator             string              `json:"numerator"`
	Denominator           string              `json:"denominator,omitempty"`
	PriceType             string              `json:"priceType"`
	MaxQuoteAgeMs         int64               `json:"maxQuoteAgeMs"`
	SessionPolicy         string              `json:"sessionPolicy"`
	Volume                string              `json:"volume"`
	Approximate           bool                `json:"approximate,omitempty"`
	ObservationResolution string              `json:"observationResolution,omitempty"`
	HistorySource         string              `json:"historySource,omitempty"`
	Segments              []ProvenanceSegment `json:"segments,omitempty"`
}

type ProvenanceSegment struct {
	From int64  `json:"from"`
	To   int64  `json:"to"`
	Kind string `json:"kind"`
}

func copyProvenance(value *DataProvenance) *DataProvenance {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Segments = append([]ProvenanceSegment(nil), value.Segments...)
	return &copy
}

func validateDataProvenance(value *DataProvenance) error {
	if value == nil {
		return nil
	}
	if value.Version != 1 || value.Numerator == "" || len(value.Numerator) > 128 || len(value.Denominator) > 128 || value.PriceType != "bid" || value.MaxQuoteAgeMs < 0 || value.MaxQuoteAgeMs > 86_400_000 || len(value.SessionPolicy) > 1024 || len(value.ObservationResolution) > 256 {
		return errors.New("invalid synthetic dataset provenance")
	}
	if value.HistorySource != "" && value.HistorySource != "dukascopy-node" && value.HistorySource != "jforex-sdk" && value.HistorySource != "mixed" {
		return errors.New("invalid Dukascopy history source provenance")
	}
	if value.Kind != "mixed-native-ratio" && len(value.Segments) != 0 {
		return errors.New("segmented synthetic provenance must identify its mixed origins")
	}
	if value.Kind == "native" && (value.Volume == "provider" || value.Volume == "unavailable") {
		return nil
	}
	if value.Kind == "synchronized-bid-ratio" && value.Denominator != "" && value.Volume == "unavailable" && value.SessionPolicy != "" && value.Approximate && value.ObservationResolution != "" {
		return nil
	}
	if value.Kind == "mixed-native-ratio" && value.Denominator != "" && value.Volume == "unavailable" && value.SessionPolicy != "" && value.Approximate && value.ObservationResolution != "" && len(value.Segments) > 1 && len(value.Segments) <= 512 {
		var native, ratio bool
		var previous int64
		for _, segment := range value.Segments {
			if segment.From < 0 || segment.To <= segment.From || segment.To > 8_640_000_000_000_000 || segment.From < previous {
				return errors.New("invalid synthetic dataset provenance segments")
			}
			switch segment.Kind {
			case "native":
				native = true
			case "synchronized-bid-ratio":
				ratio = true
			default:
				return errors.New("invalid synthetic dataset provenance segment origin")
			}
			previous = segment.To
		}
		if native && ratio {
			return nil
		}
	}
	return errors.New("unsupported synthetic dataset provenance")
}

func validateSyntheticDataset(symbol, period string, value *DataProvenance) error {
	if err := validateDataProvenance(value); err != nil {
		return err
	}
	definition, found := SyntheticDefinitionFor(symbol)
	if definition.Calculated && (value == nil || value.Kind != "synchronized-bid-ratio") {
		return errors.New("calculated synthetic worker did not report its synchronized BID provenance")
	}
	if value != nil && value.Kind != "native" {
		if !found || value.Numerator != definition.Numerator || value.Denominator != definition.Denominator || period != "d1" && period != "w1" {
			return errors.New("calculated provenance belongs to another instrument or unsupported period")
		}
	}
	return nil
}

func syntheticData(symbol string, provenance *DataProvenance) bool {
	return calculatedSynthetic(symbol) || provenance != nil && (provenance.Kind == "synchronized-bid-ratio" || provenance.Kind == "mixed-native-ratio")
}

func syntheticBasis(synthetic bool) string {
	if synthetic {
		return SyntheticSimulationBasis
	}
	return ""
}

func withSyntheticCatalog(c Catalog) Catalog {
	for index := range c.Groups {
		items := make([]Instrument, 0, len(c.Groups[index].Instruments))
		for _, item := range c.Groups[index].Instruments {
			if _, family := SyntheticDefinitionFor(item.ID); !family {
				items = append(items, item)
			}
		}
		c.Groups[index].Instruments = items
	}
	group := Group{Name: SyntheticCategory, Instruments: []Instrument{}}
	for _, definition := range syntheticDefinitions {
		formula := "Native Dukascopy BID quotes"
		note := "Native provider data is preferred; this category offers D1 and Monday-aligned W1."
		if definition.Denominator != "" {
			formula = strings.ToUpper(definition.Numerator) + " / " + strings.ToUpper(definition.Denominator)
		}
		if definition.Calculated {
			note = "Approximate ratio of common-timestamp BID observations; volume unavailable. " + SyntheticSimulationBasis
		}
		group.Instruments = append(group.Instruments, Instrument{ID: definition.ID, Name: definition.Name, EarliestDate: definition.EarliestDate,
			NativeID: definition.NativeID, Synthetic: definition.Calculated, Calculated: definition.Calculated, Category: SyntheticCategory,
			SupportedTimeframes: []string{"d1", "w1"}, Formula: formula, Note: note})
	}
	c.Groups = append(c.Groups, group)
	return c
}
