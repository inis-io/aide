package apis

import (
	"context"
	"testing"
)

// fakeDoer - 记录调用的假宿主（断言 New 装配的调用能力）
type fakeDoer struct {
	calls int
}

func (this *fakeDoer) Do(ctx context.Context, method string, path string, body []byte) ([]byte, error) {
	this.calls++
	return []byte(`{"code":200}`), nil
}

// TestNewAssemblesDoer - New 仅装配宿主调用能力，不发起网络请求
func TestNewAssemblesDoer(t *testing.T) {

	doer := &fakeDoer{}
	client := New(doer)
	if client == nil || client.doer != doer {
		t.Fatalf("New 应原样装配 doer")
	}
	if doer.calls != 0 {
		t.Fatalf("New 不得发起调用，实际 %d 次", doer.calls)
	}
}
