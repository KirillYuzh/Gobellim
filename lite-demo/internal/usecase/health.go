package usecase

import "some-project/internal/entity"

// Health отдаёт состояние сервиса: каталог инструментов и бюджет.
func (u *UseCase) Health() entity.Health {
	return entity.Health{
		Status: "ok",
		Tools:  u.agent.Tools().Names(),
		Budget: u.agent.Budget(),
	}
}
