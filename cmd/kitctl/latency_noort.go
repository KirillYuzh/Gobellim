//go:build !ort

package main

import (
	"errors"

	"github.com/gobellim/kit/guard"
)

func newL2Detector(string, string) (guard.Detector, error) {
	return nil, errors.New("L2 requires the ort build tag and the native ONNX runtime; rebuild with -tags ort")
}
