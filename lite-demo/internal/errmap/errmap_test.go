package errmap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"some-project/internal/entity"
)

func TestStatusAndMessage(t *testing.T) {
	cases := []struct {
		err    error
		status int
		msg    string
	}{
		{fmt.Errorf("wrapped: %w", entity.ErrInvalid), http.StatusBadRequest, "invalid request"},
		{entity.ErrUnauthorized, http.StatusForbidden, "forbidden"},
		{entity.ErrNotFound, http.StatusNotFound, "not found"},
		{entity.ErrBudget, http.StatusTooManyRequests, "budget exceeded"},
		{entity.ErrUpstream, http.StatusBadGateway, "upstream unavailable"},
		{context.DeadlineExceeded, http.StatusGatewayTimeout, "request timeout"},
		{context.Canceled, 499, "request canceled"},
		{errors.New("secret internal detail"), http.StatusInternalServerError, "internal error"},
	}
	for _, tc := range cases {
		if got := Status(tc.err); got != tc.status {
			t.Errorf("Status(%v) = %d, want %d", tc.err, got, tc.status)
		}
		if got := Message(tc.err); got != tc.msg {
			t.Errorf("Message(%v) = %q, want %q", tc.err, got, tc.msg)
		}
	}
}
