package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/observability"
	corepostgres "github.com/ZheglY/family_tree_app/internal/core/postgres"
	s3storage "github.com/ZheglY/family_tree_app/internal/core/storage/s3"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/server"
	"github.com/ZheglY/family_tree_app/internal/features/auth/access"
	identityclient "github.com/ZheglY/family_tree_app/internal/features/auth/identity"
	authratelimit "github.com/ZheglY/family_tree_app/internal/features/auth/ratelimit"
	authservice "github.com/ZheglY/family_tree_app/internal/features/auth/service"
	authhttp "github.com/ZheglY/family_tree_app/internal/features/auth/transport"
	exportpostgres "github.com/ZheglY/family_tree_app/internal/features/exports/repository/postgres"
	exportservice "github.com/ZheglY/family_tree_app/internal/features/exports/service"
	exporthttp "github.com/ZheglY/family_tree_app/internal/features/exports/transport"
	healthrepository "github.com/ZheglY/family_tree_app/internal/features/health/repository"
	healthservice "github.com/ZheglY/family_tree_app/internal/features/health/service"
	healthhttp "github.com/ZheglY/family_tree_app/internal/features/health/transport"
	mediapostgres "github.com/ZheglY/family_tree_app/internal/features/media/repository/postgres"
	mediaservice "github.com/ZheglY/family_tree_app/internal/features/media/service"
	mediahttp "github.com/ZheglY/family_tree_app/internal/features/media/transport"
	personpostgres "github.com/ZheglY/family_tree_app/internal/features/persons/repository/postgres"
	personservice "github.com/ZheglY/family_tree_app/internal/features/persons/service"
	personhttp "github.com/ZheglY/family_tree_app/internal/features/persons/transport"
	relationpostgres "github.com/ZheglY/family_tree_app/internal/features/relationships/repository/postgres"
	relationservice "github.com/ZheglY/family_tree_app/internal/features/relationships/service"
	relationhttp "github.com/ZheglY/family_tree_app/internal/features/relationships/transport"
	treepostgres "github.com/ZheglY/family_tree_app/internal/features/trees/repository/postgres"
	treeservice "github.com/ZheglY/family_tree_app/internal/features/trees/service"
	treehttp "github.com/ZheglY/family_tree_app/internal/features/trees/transport"
	unionpostgres "github.com/ZheglY/family_tree_app/internal/features/unions/repository/postgres"
	unionservice "github.com/ZheglY/family_tree_app/internal/features/unions/service"
	unionhttp "github.com/ZheglY/family_tree_app/internal/features/unions/transport"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
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

	postgresConfig, err := corepostgres.LoadConfig()
	if err != nil {
		panic(err)
	}
	database, err := corepostgres.Open(ctx, postgresConfig, log)
	if err != nil {
		panic(err)
	}
	defer database.Close()
	metrics, err := observability.NewMetrics(database.Native())
	if err != nil {
		panic(fmt.Errorf("initialize application metrics: %w", err))
	}
	metricsConfig, err := observability.LoadConfig("API_METRICS", "127.0.0.1:9090")
	if err != nil {
		panic(err)
	}
	metricsServer, err := observability.NewServer(metricsConfig, metrics.Registry(), log)
	if err != nil {
		panic(err)
	}
	objectStorageConfig, err := s3storage.LoadConfig()
	if err != nil {
		panic(err)
	}
	objectStorage, err := s3storage.New(ctx, objectStorageConfig)
	if err != nil {
		panic(fmt.Errorf("initialize S3 object storage: %w", err))
	}
	if err := objectStorage.EnsureBucket(ctx); err != nil {
		panic(fmt.Errorf("ensure private S3 bucket: %w", err))
	}

	log.Debug("initializing features", zap.String("feature", "health"))
	healthRepository := healthrepository.NewHealthRepository(database.Ping, objectStorage.Ping)
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
	requireAccess := access.RequireAccess(accessVerifier)
	cookieConfig, err := authhttp.LoadCookieConfig()
	if err != nil {
		panic(err)
	}
	refreshCookie, err := authhttp.NewRefreshCookie(cookieConfig)
	if err != nil {
		panic(err)
	}
	rateLimitConfig, err := authratelimit.LoadConfig()
	if err != nil {
		panic(err)
	}
	authRateLimiter, err := authratelimit.New(ctx, rateLimitConfig)
	if err != nil {
		panic(fmt.Errorf("initialize auth rate limiter: %w", err))
	}
	defer func() {
		if err := authRateLimiter.Close(); err != nil {
			log.Warn("close auth rate limiter", zap.Error(err))
		}
	}()
	authTransportHTTP := authhttp.NewHandler(
		authService,
		refreshCookie,
		requireAccess,
		authRateLimiter,
	)

	log.Debug("initializing features", zap.String("feature", "trees"))
	treeRepository := treepostgres.New(database.Native())
	treeService := treeservice.New(treeRepository)
	treeTransportHTTP := treehttp.NewHandler(treeService, requireAccess)

	log.Debug("initializing features", zap.String("feature", "persons"))
	personRepository := personpostgres.New(database.Native())
	personService := personservice.New(personRepository, treeRepository)
	personTransportHTTP := personhttp.NewHandler(personService, requireAccess)

	log.Debug("initializing features", zap.String("feature", "relationships"))
	relationRepository := relationpostgres.New(database.Native())
	relationService := relationservice.New(relationRepository, treeRepository)
	relationTransportHTTP := relationhttp.NewHandler(relationService, requireAccess)

	log.Debug("initializing features", zap.String("feature", "unions"))
	unionRepository := unionpostgres.New(database.Native())
	unionService := unionservice.New(unionRepository, treeRepository)
	unionTransportHTTP := unionhttp.NewHandler(unionService, requireAccess)

	log.Debug("initializing features", zap.String("feature", "media"))
	mediaRepository := mediapostgres.New(database.Native())
	mediaService := mediaservice.New(
		mediaRepository,
		treeRepository,
		objectStorage,
		objectStorageConfig.MaxUploadBytes,
	)
	mediaTransportHTTP := mediahttp.NewHandler(mediaService, requireAccess)

	log.Debug("initializing features", zap.String("feature", "exports"))
	exportRepository := exportpostgres.New(database.Native())
	exportService := exportservice.New(exportRepository, treeRepository, objectStorage)
	exportTransportHTTP := exporthttp.NewHandler(exportService, requireAccess)

	log.Debug("initializing HTTP server")
	// создаем адаптер сервера
	httpConfig := server.NewConfigMust()
	browserSecurity, err := middleware.BrowserSecurity(middleware.BrowserSecurityConfig{
		AllowedOrigins:    httpConfig.AllowedOrigins,
		HSTSMaxAgeSeconds: httpConfig.HSTSMaxAgeSeconds,
		CORSMaxAgeSeconds: httpConfig.CORSMaxAgeSeconds,
	})
	if err != nil {
		panic(fmt.Errorf("initialize browser security middleware: %w", err))
	}
	httpServer := server.NewHTTPServer(
		httpConfig,
		log,
		middleware.RequestID(),
		middleware.Logger(log),
		metrics.HTTPMiddleware(),
		middleware.Recovery(),
		browserSecurity,
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
	apiV1Router.RegisterRoutes(treeTransportHTTP.Routes()...)
	apiV1Router.RegisterRoutes(personTransportHTTP.Routes()...)
	apiV1Router.RegisterRoutes(relationTransportHTTP.Routes()...)
	apiV1Router.RegisterRoutes(unionTransportHTTP.Routes()...)
	apiV1Router.RegisterRoutes(mediaTransportHTTP.Routes()...)
	apiV1Router.RegisterRoutes(exportTransportHTTP.Routes()...)
	httpServer.RegisterAPIRouters(apiV1Router)

	group, runContext := errgroup.WithContext(ctx)
	group.Go(func() error { return httpServer.Run(runContext) })
	group.Go(func() error { return metricsServer.Run(runContext) })
	if err := group.Wait(); err != nil {
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
