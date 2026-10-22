package gliner

import (
	"errors"
	"fmt"
)

// Types shared between ort and non-ort builds for testability.

type word struct {
	token string
	start int
	end   int
}

type span struct {
	text  string
	start int
	end   int
	score float64
}

type decodedClass struct {
	group int
	label string
	score float64
}

type decodeResult struct {
	entities        map[string][]span
	classifications []decodedClass
	safety          string
	score           float64
}

type modelConfig struct {
	PrefixIDs   []int64           `json:"prefix_ids"`
	CategoryMap map[string]string `json:"category_map"`
	Prompt      struct {
		PrefixIDs []int64 `json:"prefix_ids"`
	} `json:"prompt"`
	MaxSequenceLength int `json:"max_sequence_length"`
	MaxWidth          int `json:"max_width"`
	CountMax          int `json:"count_max"`
	Schema            struct {
		Entities struct {
			Labels                []string           `json:"labels"`
			Thresholds            map[string]float64 `json:"thresholds"`
			SchemaMarkerPositions []int64            `json:"schema_marker_positions"`
		} `json:"entities"`
		Classifications []classification `json:"classifications"`
	} `json:"schema"`
	Decode struct {
		DefaultSpanThreshold float64 `json:"default_span_threshold"`
	} `json:"decode"`
}

type classification struct {
	Task                  string             `json:"task"`
	Labels                []string           `json:"labels"`
	MultiLabel            bool               `json:"multi_label"`
	Activation            string             `json:"activation"`
	Output                string             `json:"output"`
	ClsThreshold          float64            `json:"cls_threshold"`
	Threshold             float64            `json:"threshold"`
	Thresholds            map[string]float64 `json:"thresholds"`
	SchemaMarkerPositions []int64            `json:"schema_marker_positions"`
}

func (c *classification) activation() string {
	if c.Activation == "" {
		return "softmax"
	}
	return c.Activation
}

func (c *classification) outputName() string {
	if c.Output == "" {
		return "cls_logits"
	}
	return c.Output
}

func (c *classification) thresholdFor(label string) float64 {
	return thresholdFor(c.ClsThreshold, c.Threshold, c.Thresholds, label)
}

func (mc *modelConfig) classSchemaIndices() []int64 {
	var out []int64
	for gi := range mc.Schema.Classifications {
		out = append(out, int64(gi))
	}
	return out
}

func (mc *modelConfig) classOutputs() []string {
	var out []string
	for _, g := range mc.Schema.Classifications {
		out = append(out, g.outputName())
	}
	return out
}

func (mc *modelConfig) validate() error {
	if len(mc.Schema.Classifications) == 0 {
		return errors.New("model config: no classification groups")
	}
	for gi, g := range mc.Schema.Classifications {
		if len(g.Labels) == 0 {
			return fmt.Errorf("model config: classification group %d has no labels", gi)
		}
		if g.MultiLabel && g.activation() != "sigmoid" {
			return fmt.Errorf("model config: classification group %d is multi_label but activation is %q", gi, g.activation())
		}
		if len(g.SchemaMarkerPositions) == 0 {
			return fmt.Errorf("model config: classification group %d has no schema marker positions", gi)
		}
	}
	if len(mc.Schema.Entities.Labels) == 0 {
		return errors.New("model config: no entity labels")
	}
	for label, cat := range mc.CategoryMap {
		if _, ok := parseCategory(cat); !ok && !categoryIsSafe(cat) {
			return fmt.Errorf("model config: category %q for label %q is not a valid guard category", cat, label)
		}
	}
	return nil
}

type prepared struct {
	runes     []rune
	words     []word
	nRunes    int
	inputIDs  []int64
	wordFirst []int64
	attn      []int64
}

type outputs struct {
	spanProbs   []float32
	countLogits []float32
	clsLogits   [][]float32
}
