// SPDX-License-Identifier: MIT

package adminapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// MaxRequestBytes bounds an admin request body.
const MaxRequestBytes = 16 << 10

// Decode reads one JSON object from the request body into v, refusing unknown fields, trailing
// data and bodies over MaxRequestBytes. Its error is a validation problem.
func Decode(r *http.Request, v any) *Problem {
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxRequestBytes+1))
	if err != nil {
		return NewProblem(http.StatusBadRequest, SlugValidation, "The request body could not be read.")
	}
	if len(data) > MaxRequestBytes {
		return NewProblem(http.StatusBadRequest, SlugValidation, "The request body is too large.")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return NewProblem(http.StatusBadRequest, SlugValidation, fmt.Sprintf("The request body is not valid: %v.", err))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return NewProblem(http.StatusBadRequest, SlugValidation, "The request body has data after the JSON object.")
	}
	return nil
}
