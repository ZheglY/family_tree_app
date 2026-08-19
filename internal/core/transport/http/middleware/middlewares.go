package middleware

import (
	"net/http"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/requestid"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

/*
Middleware — это тип функции, которая принимает handler
и возвращает новый handler. То есть:

h = RequestID()(Logger(Trace(handler)))
RequestID → Logger → Trace → handler

Три слоя привязки мидлварь:

HTTPServer middleware — общие для приложения
APIVersionRouter middleware — общие для одной версии
Route middleware — только для одного endpoint
*/
type Middleware func(http.Handler) http.Handler

/*
Обвешивает handler мидлварями
Цикл идёт с конца, потому что каждая новая обёртка помещается
снаружи предыдущей.
*/
func ChainMiddleware(
	h http.Handler, // конечный handler, который требуется обернуть
	m ...Middleware, // произвольное количество middleware
) http.Handler {
	if len(m) == 0 {
		return h
	}

	for i := len(m) - 1; i >= 0; i-- {
		h = m[i](h)
	}

	return h
}

// Создает уникальный индентификатор поступишего запроса и
// сохраняет его в заголовок запроса для middleware Trace
func RequestID() Middleware {
	return func(next http.Handler) http.Handler { // Возвращается middleware, которая запоминает следующий handler в переменной next.
		return http.HandlerFunc(
			func(rw http.ResponseWriter, r *http.Request) {
				requestID := r.Header.Get(requestid.Header)
				if requestID == "" {
					requestID = uuid.NewString() // генерация уникалного id
				}

				// Меняем поле заголовка на новый ID для других мидлварь
				r.Header.Set(requestid.Header, requestID)

				// Сразу формируем ответный json с айдишником для фронта
				rw.Header().Set(requestid.Header, requestID)

				ctx := requestid.NewContext(r.Context(), requestID)
				next.ServeHTTP(rw, r.WithContext(ctx))
			})
	}
}

// Кладет логгер в конекст для возможности использования одного
// логера во всех слоях обработки запроса
// log *logger.Logger - был создан один раз в main.
func Logger(log *logger.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(
			func(rw http.ResponseWriter, r *http.Request) {
				requestID := requestid.FromContext(r.Context())

				// Изменяет вывод логов
				l := log.With(
					zap.String("request_id", requestID),
					zap.String("url", r.URL.String()),
				)

				// Сохраняем логер в контекст
				ctx := logger.ToContext(
					r.Context(),
					l,
				)

				// r.WithContext(ctx) - создаёт копию HTTP-запроса с новым контекстом
				requestWithLogger := r.WithContext(ctx)
				// Копия запроса передаётся следующему handler
				next.ServeHTTP(rw, requestWithLogger)
			})
	}
}

// Визуально удобное отображение информации о запросе
func Trace() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(
			func(rw http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				log := logger.FromContext(ctx)
				responseWriter := response.NewResponseWriter(rw)

				before := time.Now()
				log.Debug(
					">>> incoming HTTP request",
					zap.String("http_method", r.Method),
					zap.Time("time", before.UTC()),
				)

				next.ServeHTTP(responseWriter, r)

				log.Debug(
					"<<< done HTTP request",
					zap.Int("status_code", responseWriter.GetStatusCode()),
					zap.Duration("latency", time.Since(before)),
				)
			},
		)
	}
}

// middleware которая перехватывает неожиданный panic в
// последующих middleware или конечном handler, записывает
// информацию в лог и возвращает клиенту контролируемый ответ 500.
func Recovery() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			log := logger.FromContext(ctx)
			responseHandler := response.NewHTTPResponseHandler(log, rw)

			defer func() {
				if p := recover(); p != nil {
					responseHandler.PanicResponse(
						p,
						"during handler HTTP request got unexpected panic",
					)
				}
			}()

			next.ServeHTTP(rw, r)
		})
	}
}

// BodyLimit bounds JSON and form request bodies handled by the API. Large
// media files are uploaded directly to object storage via presigned URLs.
func BodyLimit(maxBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(rw, r.Body, maxBytes)
			next.ServeHTTP(rw, r)
		})
	}
}
