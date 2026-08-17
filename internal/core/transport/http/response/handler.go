package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/requestid"
	"go.uber.org/zap"
)

// Обертка над Response Writer для удобной отправки ответов сервера
type HTTPResponseHandler struct {
	log *logger.Logger
	rw  http.ResponseWriter
}

type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details"`
	RequestID string         `json:"request_id,omitempty"`
}

func NewHTTPResponseHandler(
	log *logger.Logger,
	rw http.ResponseWriter,
) *HTTPResponseHandler {
	return &HTTPResponseHandler{
		log: log,
		rw:  rw,
	}
}

// Эта функция формирует HTTP-ответ: устанавливает статус-код,
// преобразует данные в JSON и отправляет их клиенту.
func (h *HTTPResponseHandler) JSONResponse(
	responseBody any,
	statusCode int,
) {
	h.rw.Header().Set("Content-Type", "application/json; charset=utf-8")

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
		errorCode  string
		logFunc    func(string, ...zap.Field)
	)

	switch {
	case errors.Is(err, apperrors.ErrInvalidArgument):
		statusCode = http.StatusBadRequest
		errorCode = "invalid_argument"
		logFunc = h.log.Warn

	case errors.Is(err, apperrors.ErrUnauthorized),
		errors.Is(err, apperrors.ErrInvalidCredentials):
		statusCode = http.StatusUnauthorized
		errorCode = "unauthorized"
		logFunc = h.log.Debug

	case errors.Is(err, apperrors.ErrForbidden):
		statusCode = http.StatusForbidden
		errorCode = "forbidden"
		logFunc = h.log.Debug

	case errors.Is(err, apperrors.ErrNotFound):
		statusCode = http.StatusNotFound
		errorCode = "not_found"
		logFunc = h.log.Debug

	case errors.Is(err, apperrors.ErrConflict),
		errors.Is(err, apperrors.ErrEmailAlreadyTaken):
		statusCode = http.StatusConflict
		errorCode = "conflict"
		logFunc = h.log.Warn

	case errors.Is(err, apperrors.ErrUnprocessable):
		statusCode = http.StatusUnprocessableEntity
		errorCode = "unprocessable_entity"
		logFunc = h.log.Warn

	case errors.Is(err, apperrors.ErrTooManyRequests):
		statusCode = http.StatusTooManyRequests
		errorCode = "too_many_requests"
		logFunc = h.log.Warn

	case errors.Is(err, apperrors.ErrServiceUnavailable):
		statusCode = http.StatusServiceUnavailable
		errorCode = "service_unavailable"
		logFunc = h.log.Warn

	default:
		statusCode = http.StatusInternalServerError
		errorCode = "internal_error"
		logFunc = h.log.Error
	}

	logFunc(msg, zap.Error(err))

	if statusCode == http.StatusInternalServerError {
		msg = "Internal server error"
	}

	h.errorResponse(statusCode, errorCode, msg)
}

func (h *HTTPResponseHandler) PanicResponse(p any, msg string) {
	statusCode := http.StatusInternalServerError
	err := fmt.Errorf("unexpected panic: %v", p)

	h.log.Error(msg, zap.Error(err))

	h.errorResponse(statusCode, "internal_error", "Internal server error")
}

func (h *HTTPResponseHandler) errorResponse(
	statusCode int,
	errorCode string,
	msg string,
) {
	response := ErrorEnvelope{
		Error: ErrorBody{
			Code:      errorCode,
			Message:   msg,
			Details:   map[string]any{},
			RequestID: h.rw.Header().Get(requestid.Header),
		},
	}

	h.JSONResponse(response, statusCode)
}
