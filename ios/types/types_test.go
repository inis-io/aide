package types

import (
	"encoding/json"
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

// TestBucketInfoSharedFields - 公共桶契约字段按服务端 camelCase 解析；旧响应缺字段时归零（向后兼容）
func TestBucketInfoSharedFields(t *testing.T) {

	raw := `{
		"name": "emoji",
		"region": "us-east-1",
		"acl": "public-read",
		"versioning": "off",
		"status": "active",
		"storageClass": "STANDARD",
		"shared": true,
		"sharePrefix": "community/",
		"shareMaxObjects": 500,
		"shareMaxBytes": 524288000,
		"allowExts": ".png,.apng,.gif,.webp,.jpg,.jpeg,.json",
		"createAt": 1700000000000,
		"updateAt": 1700000000001
	}`

	var info BucketInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatalf("BucketInfo 解析失败：%v", err)
	}
	if !info.Shared || info.SharePrefix != "community/" ||
		info.ShareMaxObjects != 500 || info.ShareMaxBytes != 524288000 ||
		info.AllowExts != ".png,.apng,.gif,.webp,.jpg,.jpeg,.json" {
		t.Fatalf("公共桶字段解析不符合预期：%+v", info)
	}

	// 老版本服务端响应不含新字段：解析应成功且字段保持零值（追加式契约）
	var legacy BucketInfo
	if err := json.Unmarshal([]byte(`{"name":"photos","acl":"private"}`), &legacy); err != nil {
		t.Fatalf("旧版 BucketInfo 解析失败：%v", err)
	}
	if legacy.Shared || legacy.SharePrefix != "" || legacy.ShareMaxObjects != 0 ||
		legacy.ShareMaxBytes != 0 || legacy.AllowExts != "" {
		t.Fatalf("旧版响应缺字段时应保持零值：%+v", legacy)
	}
}
