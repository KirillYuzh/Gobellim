//go:build ort

package main

import (
	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/guard/gliner"
)

func newL2Detector(dir, model string) (guard.Detector, error) {
	d, err := gliner.New(gliner.Config{Dir: dir, Model: model})
	if err != nil {
		return nil, err
	}
	return d, nil
}
