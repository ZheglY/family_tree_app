package healthhttp

import (
	"net/http"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
)

type HealthDTOResponse struct {
	Response string `json:"response"`
	Logs     string `json:"logs"`
}

// Проверка работоспособности приложения
func (h *HealthHandler) Live(rw http.ResponseWriter, r *http.Request) {
	h.writeHealth(rw, r, h.healthService.Live(r.Context()))
}

func (h *HealthHandler) Ready(rw http.ResponseWriter, r *http.Request) {
	h.writeHealth(rw, r, h.healthService.Ready(r.Context()))
}

func (h *HealthHandler) writeHealth(rw http.ResponseWriter, r *http.Request, healthErr error) {
	ctx := r.Context()
	log := logger.FromContext(ctx)
	responseHandler := response.NewHTTPResponseHandler(log, rw)

	if healthErr != nil {
		responseHandler.ErrorResponse(
			healthErr,
			"failed to get app health info",
		)
		return
	}

	responseHandler.JSONResponse(
		HealthDTOResponse{Response: "OK"},
		http.StatusOK,
	)
}
