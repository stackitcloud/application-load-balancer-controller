package stackit

import (
	"context"
	"fmt"
	"net/http"

	"github.com/stackitcloud/stackit-sdk-go/core/runtime"
)

// execute executes the call function and wraps the x-trace-id and X-Request-Id into the error.
func execute[T any](ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	var httpResp *http.Response
	ctx = runtime.WithCaptureHTTPResponse(ctx, &httpResp)

	resp, err := call(ctx)
	if err == nil {
		return resp, nil
	}

	traceID := runtime.GetTraceId(ctx)
	err = wrapError(err, "trace-id", traceID)

	// Some APIs like IaaS have a requestID which we can add if available.
	if httpResp != nil {
		requestID := httpResp.Header.Get("X-Request-Id")
		err = wrapError(err, "request-id", requestID)
	}

	return resp, err
}

func wrapError(err error, name, id string) error {
	if err == nil {
		return nil
	}
	if id == "" {
		return err
	}
	return fmt.Errorf("[%s:%s]: %w", name, id, err)
}
