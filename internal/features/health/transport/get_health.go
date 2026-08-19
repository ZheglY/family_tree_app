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
func (h *HealthHadler) GetHealth(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logger.FromContext(ctx)
	responseHandler := response.NewHTTPResponseHandler(log, rw)

	if err := h.healthService.GetHealth(ctx); err != nil {
		responseHandler.ErrorResponse(
			err,
			"failed to get app health info",
		)
		return
	}

	responseHandler.JSONResponse(
		HealthDTOResponse{Response: "OK"},
		http.StatusOK,
	)
}
