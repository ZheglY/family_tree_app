package healthhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
)


type HealthDTOResponse struct {
	Response string `json:"response"`
	Logs string `json:"logs"`
}

// Это HandleFunc которая далее будет использов
func (h *HealthHadler) GetHealth(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	err := h.healthService.GetHealth(ctx)
	fmt.Printf("Уровень транспорта работает исправно")

	rw.WriteHeader(200)
	json.NewEncoder(rw).Encode(HealthDTOResponse{
		Response: "ok",
		Logs: fmt.Sprintf("Logs: %v", err),
	})
}