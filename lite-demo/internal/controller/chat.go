package controller

import (
	"net/http"

	"some-project/internal/entity"
	"some-project/internal/errmap"
	"some-project/pkg/rest"
)

// Chat обрабатывает POST /v1/chat.
func (c *Controller) Chat(w http.ResponseWriter, r *http.Request) {
	var req entity.ChatRequest
	if !c.decode(w, r, &req) {
		return
	}
	ctx, cancel := c.ctx(r)
	defer cancel()

	res, err := c.uc.Chat(ctx, req)
	if err != nil {
		errmap.Write(w, c.log, err)
		return
	}
	rest.JSON(w, http.StatusOK, res)
}

// Followup обрабатывает POST /v1/followup.
func (c *Controller) Followup(w http.ResponseWriter, r *http.Request) {
	var req entity.FollowupRequest
	if !c.decode(w, r, &req) {
		return
	}
	ctx, cancel := c.ctx(r)
	defer cancel()

	res, err := c.uc.Followup(ctx, req)
	if err != nil {
		errmap.Write(w, c.log, err)
		return
	}
	rest.JSON(w, http.StatusOK, res)
}
