package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type apiProblem struct {
	Status  int
	Code    string
	Message string
}

func (e *apiProblem) Error() string { return e.Message }

func problem(status int, code, message string) error {
	return &apiProblem{Status: status, Code: code, Message: message}
}

type apiErrorBody struct {
	Timestamp time.Time `json:"timestamp"`
	Status    int       `json:"status"`
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Path      string    `json:"path"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, err error) {
	var api *apiProblem
	if !errors.As(err, &api) {
		api = &apiProblem{Status: 500, Code: "INTERNAL_ERROR", Message: "O LocalDrop encontrou uma falha interna."}
	}
	if api.Status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	writeJSONStatus(w, api.Status, apiErrorBody{
		Timestamp: time.Now().UTC(),
		Status:    api.Status,
		Code:      api.Code,
		Message:   api.Message,
		Path:      r.URL.Path,
	})
}

func writeJSON(w http.ResponseWriter, value any) {
	writeJSONStatus(w, http.StatusOK, value)
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		return problem(http.StatusBadRequest, "INVALID_REQUEST", "A solicitação não contém dados JSON válidos.")
	}
	return nil
}
