package licence

import (
	"context"
	"errors"
)

// runtimeTransport - 运行面底层传输抽象。
type runtimeTransport interface {
	RoundTrip(ctx context.Context, method, requestURI string, body []byte, withSign bool) (int, []byte, error)
	Close() error
}

func newRuntimeTransport(client *Client) (runtimeTransport, error) {
	switch client.options.Transport {
	case "", TransportHTTP:
		client.options.Transport = TransportHTTP
		return newHTTPRuntimeTransport(client), nil
	case TransportGRPC:
		return newGRPCRuntimeTransport(client)
	default:
		return nil, errors.New("不支持的运行面传输协议：" + string(client.options.Transport))
	}
}
