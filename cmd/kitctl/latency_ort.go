//go:build ort

package main

import (
	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/guard/gliner"
)

func newL2Detector(dir, model string) (guard.Detector, error) {
	d, err := gliner.New(gliner.Config{Dir: dir, Model: model})
	if err != nil {
		return nil, err
	}
	return d, nil
}
