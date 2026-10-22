package controller

import (
	"net/http"

	"some-project/pkg/rest"
)

// Health handles GET /healthz and reports the compiled catalog and the budget.
func (c *Controller) Health(w http.ResponseWriter, _ *http.Request) {
	rest.JSON(w, http.StatusOK, c.uc.Health())
}
