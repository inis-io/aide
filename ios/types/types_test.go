package types

import (
	"errors"
	"reflect"
	"testing"
)

// TestNormalizeDefaults - 默认值注入（签名前调用，两条链路共用）
func TestNormalizeDefaults(t *testing.T) {

	share := &ShareCreateRequest{Bucket: "photos", Key: "a/b.jpg", Expires: -5}
	share.Normalize()
	if share.Expires != 0 {
		t.Fatalf("expires 负值应归一为 0：got=%d", share.Expires)
	}

	shareOk := &ShareCreateRequest{Bucket: "photos", Key: "a/b.jpg", Expires: 3600}
	shareOk.Normalize()
	if shareOk.Expires != 3600 {
		t.Fatalf("expires 正值不应被改写：got=%d", shareOk.Expires)
	}

	recycle := &RecycleListRequest{}
	recycle.Normalize()
	if recycle.Page != 1 || recycle.Limit != 20 {
		t.Fatalf("回收站默认值注入失败：page=%d limit=%d", recycle.Page, recycle.Limit)
	}

	recycleCustom := &RecycleListRequest{Page: 3, Limit: 50}
	recycleCustom.Normalize()
	if recycleCustom.Page != 3 || recycleCustom.Limit != 50 {
		t.Fatalf("自定义分页不应被改写：page=%d limit=%d", recycleCustom.Page, recycleCustom.Limit)
	}
}

// TestJoinParseIds - ID 列表与契约字符串互转（签名参数集限定标量）
func TestJoinParseIds(t *testing.T) {

	if got := JoinIds([]int64{1, 2, 3}); got != "1,2,3" {
		t.Fatalf("JoinIds 失败：got=%q", got)
	}
	if got := JoinIds(nil); got != "" {
		t.Fatalf("空列表应得到空串：got=%q", got)
	}

	ids, err := ParseIds("1, 2,3")
	if err != nil || !reflect.DeepEqual(ids, []int64{1, 2, 3}) {
		t.Fatalf("ParseIds 失败：ids=%v err=%v", ids, err)
	}

	empty, err := ParseIds("  ")
	if err != nil || len(empty) != 0 {
		t.Fatalf("空串应解析为空列表：ids=%v err=%v", empty, err)
	}

	if _, err = ParseIds("1,abc"); err == nil {
		t.Fatalf("非法数字段应返回错误")
	}
}

// TestAPIError - 业务错误模型（含 nil 接收者）
func TestAPIError(t *testing.T) {

	var nilErr *APIError
	if nilErr.Error() == "" {
		t.Fatalf("nil 接收者 Error() 不应返回空串")
	}

	item := &APIError{Code: 403, Msg: "配额不足"}
	text := item.Error()
	if text == "" || !errors.Is(error(item), item) {
		t.Fatalf("Error() 异常：%q", text)
	}
}

// TestSentinelErrors - 哨兵错误可用 errors.Is 判定
func TestSentinelErrors(t *testing.T) {

	wrapped := errors.Join(ErrInvalidSign, errors.New("细节"))
	if !errors.Is(wrapped, ErrInvalidSign) {
		t.Fatalf("errors.Is 应命中 ErrInvalidSign")
	}
}
