package main

import (
	"context"
	"os/signal"
	"syscall"

	healthrepository "github.com/ZheglY/family_tree_app/internal/core/features/health/repository"
	healthservice "github.com/ZheglY/family_tree_app/internal/core/features/health/service"
	healthhttp "github.com/ZheglY/family_tree_app/internal/core/features/health/transport"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/server"
)

func main() {
	/*
	signal.NotifyContext - Создаёт дочерний контекст, который отменится при одном из событий:
    1. отменился родительский контекст;
    2. процесс получил один из перечисленных сигналов;
	3. была вызвана функция cancel.
	*/
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT, syscall.SIGTERM,
	)
	defer stop() //Гарантирует освобождение ресурсов которые 
	// signal.NotifyContext использует для подписки на сигналы.

	healthRepository := healthrepository.NewHealthRepository("pool")
	healthService := healthservice.NewHealthService(healthRepository)
	healthTransportHTTP := healthhttp.NewHealthHTTPHandler(healthService)

	// создаем адаптер сервера
	httpServer := server.NewHTTPServer(
		server.NewConfigMust(),
	)

	/* 
	apiVersionRouter - создает сущность которая хранит в себе
	версию API (v1) и встроенный mux который привязывает Роуты 
	конкретных фитч к главному mux сервера с помощью RegisterRoutes

	---
	Весь API-роутер регистрируется в главном mux под префиксом 
	/api/v1/. При запросе главный mux выбирает 
	API-версию, StripPrefix удаляет префикс, 
	а внутренний mux выбирает конкретный маршрут и 
	вызывает handler.
	*/
	apiVersionRouter := server.NewAPIVersionRouter(server.ApiVersion1)
	apiVersionRouter.RegisterRoutes(healthTransportHTTP.Routes()...)
	httpServer.RegisterAPIRouters(apiVersionRouter)

	if err := httpServer.Run(ctx); err != nil {
		panic(err)
	}
}

/*
При получении запроса POST /api/v1/health главный ServeMux сервера 
находит зарегистрированный префикс /api/v1/ и передаёт запрос 
обработчику, созданному через StripPrefix. 
StripPrefix удаляет из пути /api/v1, после чего получается 
POST /health, и передаёт изменённый запрос в APIVersionRouter. Встроенный в него ServeMux ищет сохранённый маршрут POST /health, находит связанный с ним handler h.GetHealth и вызывает его для обработки запроса.
*/