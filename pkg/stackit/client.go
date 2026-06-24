package stackit

import (
	"errors"
	"net/http"

	oapiError "github.com/stackitcloud/stackit-sdk-go/core/oapierror"
)

var ErrorNotFound = errors.New("not found")

func isOpenAPINotFound(err error) bool {
	apiErr := &oapiError.GenericOpenAPIError{}
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotFound
}

func IsNotFound(err error) bool {
	return errors.Is(err, ErrorNotFound)
}
