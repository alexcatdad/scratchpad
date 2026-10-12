package httpapi

import "net/http"

// EntityRoute validates the one segment already decoded by the standard mux.
// Historical malformed identifiers remain exportable, but never routable.
func EntityRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !portableID(r.PathValue("id")) {
			Error(w, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid entity identifier.")
			return
		}
		next(w, r)
	}
}
