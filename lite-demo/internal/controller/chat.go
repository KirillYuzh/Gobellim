package controller

import (
	"net/http"

	"some-project/internal/entity"
	"some-project/internal/errmap"
	"some-project/pkg/rest"
)

// Chat handles POST /v1/chat.
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

// Followup handles POST /v1/followup.
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

// Tool handles POST /v1/tools/{name}.
func (c *Controller) Tool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		User entity.User    `json:"user"`
		Args map[string]any `json:"args"`
	}
	if !c.decode(w, r, &req) {
		return
	}
	ctx, cancel := c.ctx(r)
	defer cancel()

	res, err := c.uc.CallTool(ctx, req.User, r.PathValue("name"), req.Args)
	if err != nil {
		errmap.Write(w, c.log, err)
		return
	}
	rest.JSON(w, http.StatusOK, map[string]any{"result": res})
}
