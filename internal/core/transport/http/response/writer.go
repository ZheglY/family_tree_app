package response

import "net/http"

var (
	StatusCodeUninitialized = -1
)

/*
Структура обертка над http.ResponseWriter с дополнительным
полем statusCode. Чтобы на выходе middleware Trace показывала
статус код обработки запроса.
*/
type ResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func NewResponseWriter(rw http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{
		ResponseWriter: rw, // Go автоматически использует имя типа без пакета
		statusCode:     StatusCodeUninitialized,
	}
}

/*
Получение статус кода и ResponseWriter (нашей обертки)
Если handler не установил статус явно, считатаем его равным
стандартному неявному статусу 200 OK.
*/
func (rw *ResponseWriter) GetStatusCode() int {
	if rw.statusCode == StatusCodeUninitialized {
		return http.StatusOK
	}

	return rw.statusCode
}

// WriteHeader remembers the first status sent to the client. net/http ignores
// later calls, so the wrapper must do the same to keep tracing accurate.
func (rw *ResponseWriter) WriteHeader(statusCode int) {
	if rw.statusCode != StatusCodeUninitialized {
		return
	}

	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *ResponseWriter) Write(body []byte) (int, error) {
	if rw.statusCode == StatusCodeUninitialized {
		rw.WriteHeader(http.StatusOK)
	}

	return rw.ResponseWriter.Write(body)
}

// Unwrap lets http.ResponseController reach optional capabilities implemented
// by the original ResponseWriter.
func (rw *ResponseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}
