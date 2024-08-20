// Convert an http.Handler into a Lambda request handler.
// Supports Lambda Function URLs configured with buffered response mode.
// Based on https://github.com/aws/aws-lambda-go/tree/main/lambdaurl
package lambdaurl

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/cockroachdb/errors"
)

type httpResponseWriter struct {
	header http.Header
	code   int
	writer io.Writer
}

func newHTTPResponseWriter(w io.Writer) httpResponseWriter {
	return httpResponseWriter{
		header: http.Header{},
		code:   http.StatusOK,
		writer: w,
	}
}

func (w *httpResponseWriter) Header() http.Header {
	return w.header
}

func (w *httpResponseWriter) Write(p []byte) (int, error) {
	b, err := w.writer.Write(p)
	if err != nil {
		return b, errors.Wrap(err, "failed to write response")
	}
	return b, nil
}

func (w *httpResponseWriter) WriteHeader(statusCode int) {
	w.code = statusCode
}

type requestContextKey struct{}

// RequestFromContext returns the *events.LambdaFunctionURLRequest from a context.
func RequestFromContext(ctx context.Context) (*events.LambdaFunctionURLRequest, bool) {
	req, ok := ctx.Value(requestContextKey{}).(*events.LambdaFunctionURLRequest)
	return req, ok
}

// Wrap converts an http.Handler into a Lambda request handler.
func Wrap(handler http.Handler) func(context.Context, events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	return func(ctx context.Context, request events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
		var body io.Reader = strings.NewReader(request.Body)
		if request.IsBase64Encoded {
			body = base64.NewDecoder(base64.StdEncoding, body)
		}
		url := "https://" + request.RequestContext.DomainName + request.RawPath
		if request.RawQueryString != "" {
			url += "?" + request.RawQueryString
		}
		ctx = context.WithValue(ctx, requestContextKey{}, request)
		httpRequest, err := http.NewRequestWithContext(ctx, request.RequestContext.HTTP.Method, url, body)
		if err != nil {
			return events.LambdaFunctionURLResponse{}, errors.Wrap(err, "failed to create http request")
		}
		httpRequest.RemoteAddr = request.RequestContext.HTTP.SourceIP
		for k, v := range request.Headers {
			httpRequest.Header.Add(k, v)
		}

		w := strings.Builder{}
		responseWriter := newHTTPResponseWriter(&w)
		handler.ServeHTTP(&responseWriter, httpRequest)

		response := events.LambdaFunctionURLResponse{
			StatusCode: responseWriter.code,
			Body:       w.String(),
		}
		response.Headers = make(map[string]string, len(responseWriter.header))
		for k, v := range responseWriter.header {
			if k == "Set-Cookie" {
				response.Cookies = v
			} else {
				response.Headers[k] = strings.Join(v, ",")
			}
		}

		return response, nil
	}
}

func Start(handler http.Handler, options ...lambda.Option) {
	lambda.StartHandlerFunc(Wrap(handler), options...)
}
