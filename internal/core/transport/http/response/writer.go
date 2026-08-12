package response

import "net/http"

var (
	StatusCodeUninitialized = -1
)

/* 
Структура обертканад http.ResponseWriter с дополнительным 
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
		statusCode: StatusCodeUninitialized,
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