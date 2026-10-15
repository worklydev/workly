package api

import (
	"errors"
	"fmt"
	"net/http"
)

// apiError is returned by handlers and rendered by the strict handler's
// ResponseErrorHandlerFunc in the standard error shape.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.code + ": " + e.message }

func invalid(format string, args ...any) error {
	return &apiError{http.StatusBadRequest, "invalid_request", fmt.Sprintf(format, args...)}
}

func queueNotFound(name string) error {
	return &apiError{http.StatusNotFound, "queue_not_found", fmt.Sprintf("queue %q does not exist", name)}
}

func taskNotFound(id string) error {
	return &apiError{http.StatusNotFound, "task_not_found", fmt.Sprintf("task %q does not exist", id)}
}

func invalidState(format string, args ...any) error {
	return &apiError{http.StatusConflict, "invalid_state", fmt.Sprintf(format, args...)}
}

// inTask prefixes a batch item's error with its position.
func inTask(i int, err error) error {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return &apiError{apiErr.status, apiErr.code, fmt.Sprintf("tasks[%d]: %s", i, apiErr.message)}
	}
	return err
}

func payloadTooLarge(limit int64) error {
	return &apiError{http.StatusRequestEntityTooLarge, "payload_too_large",
		fmt.Sprintf("task body exceeds the %d byte limit", limit)}
}

// writeHandlerError renders err, hiding details of unexpected errors.
func writeHandlerError(w http.ResponseWriter, err error) (unexpected bool) {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		writeError(w, apiErr.status, apiErr.code, apiErr.message)
		return false
	}
	writeError(w, http.StatusInternalServerError, "internal", "internal server error")
	return true
}
