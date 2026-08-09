package licence

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
)

type adminCall struct {
	Method      string
	Path        string
	Query       url.Values
	Body        []byte
	ContentType string
	Token       string
}

type adminUpload struct {
	Path      string
	Fields    map[string]string
	FileField string
	FileName  string
	Content   io.Reader
	Token     string
}

type adminTransport interface {
	RoundTrip(context.Context, adminCall) (json.RawMessage, error)
	Upload(context.Context, adminUpload) (json.RawMessage, error)
	Close() error
}

func newAdminTransport(client *AdminClient) (adminTransport, error) {
	switch client.options.Transport {
	case "", TransportHTTP:
		client.options.Transport = TransportHTTP
		return newHTTPAdminTransport(client), nil
	case TransportGRPC:
		return newGRPCAdminTransport(client)
	default:
		return nil, errors.New("不支持的管理面传输协议：" + string(client.options.Transport))
	}
}
