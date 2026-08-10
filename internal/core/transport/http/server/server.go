package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
)

/* Структура сервера
mux *http.ServeMux -
Config - конфиг с указанием порта и SHUTDOWN
*/
type HTTPServer struct {
	mux *http.ServeMux
	config Config
	log *logger.Logger
	middleware []middleware.Middleware
}

/*
http.NewServeMux() — сокращение от multiplexer.

HTTP-мультиплексор:
1. получает HTTP-запрос;
2. смотрит путь и метод;
3. выбирает подходящий обработчик;
4. вызывает его.
*/
func NewHTTPServer(
	config Config,
	log *logger.Logger,
	middleware ...middleware.Middleware,
) *HTTPServer {
	return &HTTPServer{
		mux: http.NewServeMux(),
		config: config,
		log: log,
		middleware: middleware,
	}
}

/* 
При регистрации роутеров проходимся циклом по apiVersionRouters
У каждго роутера формируем префикс и 
*/ 
func (s *HTTPServer) RegisterAPIRouters(routers ...*APIVersionRouter) {
	for _, router := range routers {
		prefix := "/api/" + string(router.apiVersion)

		s.mux.Handle(
			prefix+"/",
			http.StripPrefix(prefix, router), // обрезает путь и привязывает роутер к пути
		)
	}
}

func (s *HTTPServer) Run(ctx context.Context) error {
	/*
	server := &http.Server - создаётся сервер стандартной библиотеки:
	Addr определяет адрес прослушивания, например :8080;
	Handler получает все входящие запросы.
	*/
	server := &http.Server{
		Addr: s.config.Addr,
		Handler: s.mux,
	}

	errCh := make(chan error, 1) // создаем канал ошибок

	/* 
	Запуск сервера происходит в горутине так как 
	ListenAndServe() это блокирующая функция. 
	Пока сервер работает, она не возвращает управление.
	*/
	go func() {
		defer close(errCh)
		fmt.Println("Приложение запущено")
		err := server.ListenAndServe()

		if !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	// Сценарий 1: сервер сам завершился с ошибкой - например, порт занят
	case err := <- errCh:
		if err != nil {
			return fmt.Errorf("listen and serve HTTP: %w", err)
		}
	
	// Сценарий 2: приложение получило сигнал остановки - например Cntr + C
	case <-ctx.Done():
		shutDownCtx, cancel := context.WithTimeout(
			context.Background(),
			s.config.ShutDownTimeout,
		)
		defer cancel()

		// graceful shutdown — корректное завершение без внезапного обрыва активной работы.
		if err := server.Shutdown(shutDownCtx); err != nil {
			_ = server.Close() // Принудительное закрытие сервера

			return fmt.Errorf("shutdown HTTP server %w", err)
		}
	}

	return nil
}


/*

POST /api/v1/health
        ↓
главный ServeMux ищет "/api/v1/"
        ↓
StripPrefix получает "/health"
        ↓
вложенный ServeMux ищет "POST /health"
        ↓
h.GetHealth


При получении запроса POST /api/v1/health главный ServeMux сервера 
находит зарегистрированный префикс /api/v1/ и передаёт запрос 
обработчику, созданному через StripPrefix. 
StripPrefix удаляет из пути /api/v1, после чего получается 
POST /health, и передаёт изменённый запрос в APIVersionRouter. 
Встроенный в него ServeMux ищет сохранённый маршрут 
POST /health, находит связанный с ним handler h.GetHealth и 
вызывает его для обработки запроса.
*/