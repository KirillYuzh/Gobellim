package controller

import (
	"net/http"

	"some-project/internal/entity"
	"some-project/internal/errmap"
	"some-project/pkg/rest"
)

// ResumeResearch продолжает приостановленный сценарий. Ответ пользователя
// приходит сюда напрямую, минуя модель.
func (c *Controller) ResumeResearch(w http.ResponseWriter, r *http.Request) {
	var req entity.ResumeRequest
	if !c.decode(w, r, &req) {
		return
	}
	ctx, cancel := c.ctx(r)
	defer cancel()

	res, err := c.uc.ResumeResearch(ctx, r.PathValue("run_id"), req)
	if err != nil {
		errmap.Write(w, c.log, err)
		return
	}
	rest.JSON(w, http.StatusOK, res)
}
