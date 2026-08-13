package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"go.uber.org/zap"
)

// Обертка над Response Writer для удобной отправки ответов сервера
type HTTPResponseHandler struct {
	log *logger.Logger
	rw http.ResponseWriter
}

func NewHTTPResponseHandler(
	log *logger.Logger,
	rw http.ResponseWriter,
) *HTTPResponseHandler {
	return &HTTPResponseHandler{
		log: log,
		rw: rw,
	}
}

// Эта функция формирует HTTP-ответ: устанавливает статус-код, 
// преобразует данные в JSON и отправляет их клиенту.
func (h *HTTPResponseHandler) JSONResponse(
	responseBody any,
	statusCode int,
) {
	// После вызова WriteHeader статус уже считается отправленным. 
	// Изменить его позднее обычно нельзя.
	h.rw.WriteHeader(statusCode) // Отправляет клиенту HTTP-статус.

	encoder := json.NewEncoder(h.rw) // Создаёт JSON-кодировщик, который будет записывать результат непосредственно в HTTP-ответ.
	
	// преобразует Go-значение в JSON, записывает JSON в h.rw
	err := encoder.Encode(responseBody)
	if err != nil {
		h.log.Error("write HTTP response", zap.Error(err))
	}
}

func (h *HTTPResponseHandler) ErrorResponse(err error, msg string) {
	var (
		statusCode int
		logFunc func(string, ...zap.Field)
	)

	switch {
	case errors.Is(err, apperrors.ErrInvalidArgument):
		statusCode = http.StatusBadRequest
		logFunc = h.log.Warn

	case errors.Is(err, apperrors.ErrNotFound):
		statusCode = http.StatusNotFound
		logFunc = h.log.Debug

	case errors.Is(err, apperrors.ErrConflict):
		statusCode = http.StatusConflict
		logFunc = h.log.Warn
	
	default:
		statusCode = http.StatusInternalServerError
		logFunc = h.log.Error
	}

	logFunc(msg, zap.Error(err))

	h.errorResponse(
		statusCode,
		err,
		msg,
	)
}

func (h *HTTPResponseHandler) PanicResponse(p any, msg string) {
	statusCode := http.StatusInternalServerError
	err := fmt.Errorf("unexpected panic: %v", p)

	h.log.Error(msg, zap.Error(err))

	h.errorResponse(
		statusCode,
		err,
		msg,
	)
}

func (h *HTTPResponseHandler) errorResponse(
	statusCode int,
	err error,
	msg string,
) {
	h.rw.WriteHeader(statusCode)

	response := map[string]string{
		"message": msg,
		"error": err.Error(),
	}

	h.JSONResponse(response, statusCode)
}
