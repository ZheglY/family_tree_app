package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ZheglY/family_tree_app/internal/features/auth/access"
	identityclient "github.com/ZheglY/family_tree_app/internal/features/auth/identity"
	authservice "github.com/ZheglY/family_tree_app/internal/features/auth/service"
	authhttp "github.com/ZheglY/family_tree_app/internal/features/auth/transport"
	healthrepository "github.com/ZheglY/family_tree_app/internal/features/health/repository"
	healthservice "github.com/ZheglY/family_tree_app/internal/features/health/service"
	healthhttp "github.com/ZheglY/family_tree_app/internal/features/health/transport"
	"go.uber.org/zap"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
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

	loggerConfig := logger.NewConfigMust()
	// Создает логер при запуске приложения
	log, err := logger.NewLogger(loggerConfig)
	if err != nil {
		fmt.Println("failed to init logger:", err)
		os.Exit(1) // Приложение немедленно завершается с кодом ошибки 1.
	}
	defer log.Close()

	log.Debug("initializing features", zap.String("feature", "health"))
	healthRepository := healthrepository.NewHealthRepository("pool")
	healthService := healthservice.NewHealthService(healthRepository)
	healthTransportHTTP := healthhttp.NewHealthHTTPHandler(healthService)

	log.Debug("initializing features", zap.String("feature", "auth"))
	identityConfig, err := identityclient.LoadConfig()
	if err != nil {
		panic(err)
	}
	identityClient, err := identityclient.NewClient(identityConfig)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := identityClient.Close(); err != nil {
			log.Warn("close Identity gRPC client", zap.Error(err))
		}
	}()
	authService := authservice.NewAuthService(identityClient)
	accessTokenKey, err := authService.GetAccessTokenKey(ctx)
	if err != nil {
		panic(fmt.Errorf("load access token public key: %w", err))
	}
	accessVerifier, err := access.NewVerifier(accessTokenKey)
	if err != nil {
		panic(fmt.Errorf("initialize access token verifier: %w", err))
	}
	cookieConfig, err := authhttp.LoadCookieConfig()
	if err != nil {
		panic(err)
	}
	refreshCookie, err := authhttp.NewRefreshCookie(cookieConfig)
	if err != nil {
		panic(err)
	}
	authTransportHTTP := authhttp.NewHandler(
		authService,
		refreshCookie,
		access.RequireAccess(accessVerifier),
	)

	log.Debug("initializing HTTP server")
	// создаем адаптер сервера
	httpConfig := server.NewConfigMust()
	httpServer := server.NewHTTPServer(
		httpConfig,
		log,
		middleware.RequestID(),
		middleware.Logger(log),
		middleware.Recovery(),
		middleware.BodyLimit(httpConfig.MaxBodyBytes),
		middleware.Trace(),
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

	// Здесь можно добавить мидлвари к конкретной api version
	httpServer.RegisterRoutes(healthTransportHTTP.Routes()...)
	apiV1Router := server.NewAPIVersionRouter(server.ApiVersion1)
	apiV1Router.RegisterRoutes(authTransportHTTP.Routes()...)
	httpServer.RegisterAPIRouters(apiV1Router)

	if err := httpServer.Run(ctx); err != nil {
		panic(err)
	}
}

/*
При получении запроса POST /api/v1/health главный ServeMux сервера
находит зарегистрированный префикс /api/v1/ и передаёт запрос
обработчику, созданному через StripPrefix.
StripPrefix удаляет из пути /api/v1, после чего получается
POST /health, и передаёт изменённый запрос в APIVersionRouter.
Встроенный в него ServeMux ищет сохранённый маршрут
POST /health, находит связанный с ним handler h.GetHealth и
вызывает его для обработки запроса.
*/

/*
r.Handler - Это конечная функция маршрута - h.GetUser
Он имеет тип - http.HandlerFunc
*/

/*
1. Дописать фитчу юзерс
2. добавить привязку мидвари логирования к серверу
3. разобраться с OpenAPI и swagger
*/
