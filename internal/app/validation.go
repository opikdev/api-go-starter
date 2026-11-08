package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/opikdev/api-go-starter/internal/problem"
)

const invalidFieldsDetail = "The request has invalid fields. See `errors` for each one, then fix them and retry."

var schemaValidationOptions = []openapi3.SchemaValidationOption{
	openapi3.MultiErrors(),
	openapi3.WithStringFormatValidator("email", openapi3.NewCallbackValidator(validateEmail)),
}

func validateEmail(value string) error {
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return errors.New("not an email address")
	}
	return nil
}

func invalidRequest(err error) *problem.Error {
	return &problem.Error{
		Slug:   problem.ValidationFailed,
		Detail: invalidFieldsDetail,
		Errors: []problem.FieldError{{Field: "body", Detail: "isn't valid JSON for this operation"}},
		Cause:  err,
	}
}

func fromValidation(err error) error {
	var securityErr *openapi3filter.SecurityRequirementsError
	if errors.As(err, &securityErr) {
		for _, cause := range securityErr.Errors {
			if !errors.Is(cause, errUnauthorized) {
				return cause
			}
		}
		return &problem.Error{
			Slug:    problem.Unauthorized,
			Detail:  "Send a valid session token in the Authorization header as `Bearer <token>`.",
			Headers: map[string]string{"WWW-Authenticate": "Bearer"},
		}
	}
	if errors.Is(err, routers.ErrPathNotFound) {
		return problem.New(problem.NotFound, "No route matches this path.")
	}
	if errors.Is(err, routers.ErrMethodNotAllowed) {
		return problem.New(problem.MethodNotAllowed, "This path doesn't support that method.")
	}
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		return problem.New(problem.PayloadTooLarge, fmt.Sprintf("The request body is over the %d-byte limit.", maxBodyBytes))
	}
	if isUnsupportedMediaType(err) {
		return problem.New(problem.UnsupportedMediaType, "Send the body as JSON, with the header Content-Type: application/json.")
	}
	return &problem.Error{
		Slug:   problem.ValidationFailed,
		Detail: invalidFieldsDetail,
		Errors: fieldErrors(err),
		Cause:  err,
	}
}

func isUnsupportedMediaType(err error) bool {
	for _, e := range flatten(err) {
		var requestErr *openapi3filter.RequestError
		if errors.As(e, &requestErr) && requestErr.RequestBody != nil &&
			(strings.HasPrefix(requestErr.Reason, "header Content-Type has unexpected value") ||
				strings.Contains(requestErr.Error(), "unsupported content type")) {
			return true
		}
	}
	return false
}

func flatten(err error) []error {
	if multi, ok := err.(openapi3.MultiError); ok { //nolint:errorlint // Only a direct MultiError, because errors.As would reach inside a RequestError.
		var all []error
		for _, e := range multi {
			all = append(all, flatten(e)...)
		}
		return all
	}
	return []error{err}
}

func fieldErrors(err error) []problem.FieldError {
	var out []problem.FieldError
	for _, e := range flatten(err) {
		var requestErr *openapi3filter.RequestError
		if !errors.As(e, &requestErr) {
			out = append(out, problem.FieldError{Field: "request", Detail: "is invalid"})
			continue
		}
		var parseErr *openapi3filter.ParseError
		if requestErr.Parameter == nil && errors.As(requestErr.Err, &parseErr) {
			out = append(out, problem.FieldError{Field: "body", Detail: "isn't valid JSON"})
			continue
		}
		found := false
		for _, leaf := range schemaLeaves(requestErr.Err) {
			out = append(out, describe(requestErr.Parameter, leaf)...)
			found = true
		}
		if !found {
			out = append(out, problem.FieldError{Field: fieldName(requestErr.Parameter, nil), Detail: "is invalid"})
		}
	}
	return out
}

func schemaLeaves(err error) []*jsonschema.ValidationError {
	if err == nil {
		return nil
	}
	if multi, ok := err.(openapi3.MultiError); ok { //nolint:errorlint // Only a direct MultiError, because errors.As would reach inside a RequestError.
		var all []*jsonschema.ValidationError
		for _, e := range multi {
			all = append(all, schemaLeaves(e)...)
		}
		return all
	}
	var schemaErr *openapi3.SchemaError
	if !errors.As(err, &schemaErr) {
		return nil
	}
	if schemaErr.Origin != nil {
		return schemaLeaves(errors.Unwrap(schemaErr.Origin))
	}
	var leaf *jsonschema.ValidationError
	if errors.As(schemaErr, &leaf) {
		return []*jsonschema.ValidationError{leaf}
	}
	return nil
}

func fieldName(parameter *openapi3.Parameter, location []string) string {
	if parameter != nil {
		return parameter.Name
	}
	if len(location) == 0 {
		return "body"
	}
	return strings.Join(location, ".")
}

func describe(parameter *openapi3.Parameter, leaf *jsonschema.ValidationError) []problem.FieldError {
	at := func(extra ...string) string {
		return fieldName(parameter, append(append([]string{}, leaf.InstanceLocation...), extra...))
	}
	switch k := leaf.ErrorKind.(type) {
	case *kind.Required:
		out := make([]problem.FieldError, 0, len(k.Missing))
		for _, name := range k.Missing {
			out = append(out, problem.FieldError{Field: at(name), Detail: "is required"})
		}
		return out
	case *kind.AdditionalProperties:
		out := make([]problem.FieldError, 0, len(k.Properties))
		for _, name := range k.Properties {
			out = append(out, problem.FieldError{Field: at(name), Detail: "isn't a known field"})
		}
		return out
	}
	return []problem.FieldError{{Field: at(), Detail: detailFor(leaf.ErrorKind)}}
}

func detailFor(errorKind jsonschema.ErrorKind) string {
	switch k := errorKind.(type) {
	case *kind.Type:
		return "must be " + strings.Join(k.Want, " or ")
	case *kind.MinLength:
		return fmt.Sprintf("must be at least %d characters", k.Want)
	case *kind.MaxLength:
		return fmt.Sprintf("must be at most %d characters", k.Want)
	case *kind.Minimum:
		return "must be at least " + k.Want.RatString()
	case *kind.Maximum:
		return "must be at most " + k.Want.RatString()
	case *kind.Format:
		return "must be a valid " + k.Want
	case *kind.Pattern:
		return "must match " + k.Want
	case *kind.Enum:
		return fmt.Sprintf("must be one of %v", k.Want)
	}
	return "is invalid"
}
