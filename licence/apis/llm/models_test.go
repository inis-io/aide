package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestModels - 模型目录：list 信封解包为 []Model
func TestModels(t *testing.T) {

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/models" || req.Method != http.MethodGet {
			t.Fatalf("路径/方法不符：%s %s", req.Method, req.URL.Path)
		}
		writer.Write([]byte(`{"object":"list","data":[
			{"id":"deepseek-chat","object":"model","created":1735689600,"owned_by":"licen-hub"},
			{"id":"deepseek-reasoner","object":"model","created":1735689600,"owned_by":"licen-hub"}
		]}`))
	}))
	defer server.Close()

	models, err := NewClient(server.URL+"/v1", "sk-abc").Models(context.Background())
	if err != nil {
		t.Fatalf("Models 失败：%v", err)
	}
	if len(models) != 2 || models[0].Id != "deepseek-chat" || models[0].OwnedBy != "licen-hub" || models[0].Created != 1735689600 {
		t.Fatalf("目录解析不符：%+v", models)
	}
}

// TestModelDetail - 模型详情：成功解析；404 归一为 *Error（model_not_found）
func TestModelDetail(t *testing.T) {

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1/models/deepseek-chat" {
			writer.Write([]byte(`{"id":"deepseek-chat","object":"model","created":1735689600,"owned_by":"licen-hub"}`))
			return
		}
		writer.WriteHeader(http.StatusNotFound)
		writer.Write([]byte(`{"error":{"message":"模型不存在或已下架","type":"invalid_request_error","code":"model_not_found"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/v1", "sk-abc")
	model, err := client.Model(context.Background(), "deepseek-chat")
	if err != nil {
		t.Fatalf("Model 失败：%v", err)
	}
	if model.Id != "deepseek-chat" || model.Object != "model" {
		t.Fatalf("详情解析不符：%+v", model)
	}
	_, err = client.Model(context.Background(), "not-exists")
	llmErr, ok := err.(*Error)
	if !ok || llmErr.Code != ErrorCodeModelNotFound || llmErr.HTTPStatus != http.StatusNotFound {
		t.Fatalf("404 应归一为 model_not_found，实际：%+v", err)
	}
}
