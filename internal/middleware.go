package internal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	applicationJSON            = "application/json"
	applicationJSONCharsetUTF8 = applicationJSON + "; charset=utf-8"
)

var contentType = http.CanonicalHeaderKey("Content-Type")

type jsonResponse struct {
	Message  string `json:",omitempty"`
	Location string `json:",omitempty"`
}

func RecoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create a deferred function (which will always be run in the event of a panic
		// as Go unwinds the stack).
		defer func() {
			// Use the builtin recover function to check if there has been a panic or
			// not.
			if rec := recover(); rec != nil {
				err, ok := rec.(error)
				if !ok {
					err = fmt.Errorf("%v", rec)
				}

				w.Header().Set("Connection", "close")
				w.WriteHeader(http.StatusInternalServerError)

				ct := r.Header.Get(contentType)
				if strings.HasPrefix(ct, applicationJSON) {
					w.Header().Set(
						contentType,
						applicationJSONCharsetUTF8,
					)

					e := jsonResponse{
						Message: err.Error(),
					}
					err := json.NewEncoder(w).Encode(e)
					if err != nil {
						panic(err.Error())
					}
				} else {
					_, err := w.Write([]byte(err.Error()))
					if err != nil {
						panic(err.Error())
					}
				}
			}
		}()

		next.ServeHTTP(w, r)
	})
}
