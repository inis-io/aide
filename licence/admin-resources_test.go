package licence

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestPaginationFind - 分页响应 {data,count,page} 解析 + 查询参数序列化（数组按 key[]=v 重复）
func TestPaginationFind(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/projects/find"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"data": []map[string]any{
				{"id": 11, "projectNo": "PRJ-000011", "projectName": "分页项目", "status": "released", "userId": 7},
			},
			"count": 42,
			"page":  2,
		})
	}
	client := hub.newClient(t)

	page, err := client.Projects.Find(context.Background(), &ProjectFindParams{
		Page: 2, Limit: 1, Status: []string{"released", "developing"},
		UserId: []int{7}, CreateTime: []int64{1700000000000, 1800000000000},
	})
	if err != nil {
		t.Fatalf("分页请求失败: %v", err)
	}
	if page.Count != 42 || page.Page != 2 || len(page.Data) != 1 {
		t.Fatalf("分页结构解析不符: %+v", page)
	}
	if page.Data[0].ProjectName != "分页项目" || page.Data[0].UserId != 7 {
		t.Fatalf("行数据解析不符: %+v", page.Data[0])
	}

	// 查询参数：标量直写，数组按平台约定序列化为 key[]=v（可重复）
	query := hub.lastQuery
	if query.Get("page") != "2" || query.Get("limit") != "1" {
		t.Fatalf("分页参数不符: %s", query.Encode())
	}
	if got := query["status[]"]; len(got) != 2 || got[0] != "released" || got[1] != "developing" {
		t.Fatalf("数组参数应为 key[]=v 重复形式: %s", query.Encode())
	}
	if got := query["userId[]"]; len(got) != 1 || got[0] != "7" {
		t.Fatalf("整型数组参数不符: %s", query.Encode())
	}
	if got := query["createTime[]"]; len(got) != 2 || got[0] != "1700000000000" {
		t.Fatalf("区间参数不符: %s", query.Encode())
	}
}

// TestProjectTake - take 接口 ?id=N 查询参数
func TestProjectTake(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/projects/take"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 3, "projectNo": "PRJ-000003"})
	}
	client := hub.newClient(t)

	row, err := client.Projects.Take(context.Background(), 3)
	if err != nil {
		t.Fatalf("详情请求失败: %v", err)
	}
	if row.Id != 3 || row.ProjectNo != "PRJ-000003" {
		t.Fatalf("详情解析不符: %+v", row)
	}
	if hub.lastQuery.Get("id") != "3" {
		t.Fatalf("take 应携带 ?id=3，实际: %s", hub.lastQuery.Encode())
	}
}

// TestProjectCreate - 新增项目：JSON body 字段与平台 types.Project 对齐
func TestProjectCreate(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/projects/create"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 9})
	}
	client := hub.newClient(t)

	result, err := client.Projects.Create(context.Background(), ProjectInput{
		ProjectName: "新项目", ProjectType: "web", LicenseMode: "online",
	})
	if err != nil {
		t.Fatalf("新增失败: %v", err)
	}
	if result.Id != 9 {
		t.Fatalf("返回 ID 不符: %+v", result)
	}

	var sent map[string]any
	if err = json.Unmarshal(hub.lastBody, &sent); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if sent["projectName"] != "新项目" || sent["projectType"] != "web" || sent["licenseMode"] != "online" {
		t.Fatalf("请求体字段不符: %s", string(hub.lastBody))
	}
	// omitempty：未填字段不应上送（平台只处理提交的参数）
	if _, ok := sent["remark"]; ok {
		t.Fatalf("未填字段不应上送: %s", string(hub.lastBody))
	}
}

// TestProjectRemove - 逻辑删除：DELETE 携带 JSON body {ids:[...]}
func TestProjectRemove(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["DELETE /api/projects/remove"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"ids": []int{1, 2}})
	}
	client := hub.newClient(t)

	result, err := client.Projects.Remove(context.Background(), []int{1, 2})
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if len(result.Ids) != 2 || result.Ids[0] != 1 {
		t.Fatalf("删除结果解析不符: %+v", result)
	}
	if !strings.Contains(string(hub.lastBody), `"ids":[1,2]`) {
		t.Fatalf("DELETE 请求体不符: %s", string(hub.lastBody))
	}
}

// TestLicenseApply - 提交授权申请：请求体与返回 {id,applyNo} 解析
func TestLicenseApply(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/licenses/apply"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 21, "applyNo": "LICAP-2026-000021"})
	}
	client := hub.newClient(t)

	result, err := client.Licenses.Apply(context.Background(), LicenseApplyInput{
		ProjectId: 11, InstanceId: 5, LicenseType: "subscription", Environment: "production",
		Reason: "生产部署", RequestPayload: map[string]any{"validUntil": 1800000000000},
	})
	if err != nil {
		t.Fatalf("申请失败: %v", err)
	}
	if result.Id != 21 || result.ApplyNo != "LICAP-2026-000021" {
		t.Fatalf("申请结果解析不符: %+v", result)
	}

	var sent map[string]any
	if err = json.Unmarshal(hub.lastBody, &sent); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if sent["projectId"] != float64(11) || sent["licenseType"] != "subscription" || sent["environment"] != "production" {
		t.Fatalf("申请请求体不符: %s", string(hub.lastBody))
	}
	if _, ok := sent["requestPayload"].(map[string]any)["validUntil"]; !ok {
		t.Fatalf("requestPayload 未上送: %s", string(hub.lastBody))
	}
}

// TestLicenseTakePayload - 查看签发载荷（载荷/签名原文）
func TestLicenseTakePayload(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/licenses/take-payload"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"licenseNo": "LIC-2026-000123", "payload": `{"licenseId":"LIC-2026-000123"}`,
			"signature": "ab12", "keyVersion": "license-key-2026-01",
		})
	}
	client := hub.newClient(t)

	view, err := client.Licenses.TakePayload(context.Background(), 123)
	if err != nil {
		t.Fatalf("载荷请求失败: %v", err)
	}
	if view.LicenseNo != "LIC-2026-000123" || view.KeyVersion != "license-key-2026-01" || view.Signature != "ab12" {
		t.Fatalf("载荷解析不符: %+v", view)
	}
	if !strings.Contains(view.Payload, "LIC-2026-000123") {
		t.Fatalf("载荷原文不符: %s", view.Payload)
	}
}

// TestSigningKeyPublic - 公钥导出：purpose/keyVersion 查询参数透传与结果解析
// release 用途不携带 projectId（全局密钥）；license 用途必带 projectId（项目级密钥）。
func TestSigningKeyPublic(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/signing-keys/public"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"purpose": "release", "keyVersion": "release-key-2026-01",
			"algorithm": "Ed25519", "publicKey": "deadbeef",
		})
	}
	client := hub.newClient(t)

	key, err := client.SigningKeys.Public(context.Background(), "release", "release-key-2026-01", 0)
	if err != nil {
		t.Fatalf("公钥导出失败: %v", err)
	}
	if key.Purpose != "release" || key.Algorithm != "Ed25519" || key.PublicKey != "deadbeef" {
		t.Fatalf("公钥解析不符: %+v", key)
	}
	query := hub.lastQuery
	if query.Get("purpose") != "release" || query.Get("keyVersion") != "release-key-2026-01" {
		t.Fatalf("查询参数不符: %s", query.Encode())
	}
	if query.Get("projectId") != "" {
		t.Fatalf("release 用途不应携带 projectId: %s", query.Encode())
	}
}

// TestSigningKeyPublicLicense - license 用途公钥导出：projectId 透传
func TestSigningKeyPublicLicense(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/signing-keys/public"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"purpose": "license", "keyVersion": "license-key-INIS-202608-abc123",
			"algorithm": "Ed25519", "publicKey": "cafebabe",
		})
	}
	client := hub.newClient(t)

	key, err := client.SigningKeys.Public(context.Background(), "license", "", 3)
	if err != nil {
		t.Fatalf("license 公钥导出失败: %v", err)
	}
	if key.Purpose != "license" || key.PublicKey != "cafebabe" {
		t.Fatalf("公钥解析不符: %+v", key)
	}
	query := hub.lastQuery
	if query.Get("projectId") != "3" {
		t.Fatalf("license 用途应携带 projectId=3: %s", query.Encode())
	}
}

// TestLicensePublicKey - 项目公钥表导出：projectId 透传 + keys[] 全版本解析
func TestLicensePublicKey(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/licenses/public-key"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"algorithm": "Ed25519", "projectId": 3, "projectNo": "INIS",
			"keys": []map[string]any{
				{"keyVersion": "license-key-2026-01", "publicKey": "deadbeef"},
				{"keyVersion": "license-key-INIS-202608-abc123", "publicKey": "cafebabe"},
			},
		})
	}
	client := hub.newClient(t)

	pub, err := client.Licenses.PublicKey(context.Background(), 3)
	if err != nil {
		t.Fatalf("项目公钥表导出失败: %v", err)
	}
	if pub.ProjectId != 3 || pub.ProjectNo != "INIS" || len(pub.Keys) != 2 {
		t.Fatalf("公钥表解析不符: %+v", pub)
	}
	if pub.Keys[1].KeyVersion != "license-key-INIS-202608-abc123" || pub.Keys[1].PublicKey != "cafebabe" {
		t.Fatalf("公钥表条目不符: %+v", pub.Keys)
	}
	query := hub.lastQuery
	if query.Get("projectId") != "3" {
		t.Fatalf("projectId 查询参数不符: %s", query.Encode())
	}
}

// TestArtifactVerify - 发布物服务端代验：JSON body {id}，结果字段解析
func TestArtifactVerify(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/project-artifacts/verify"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"id": 7, "artifactNo": "ART-2026-000007", "keyVersion": "release-key-2026-01",
			"sha256": "abc", "recomputedSha256": "", "hashMatch": true,
			"signatureValid": true, "valid": true,
		})
	}
	client := hub.newClient(t)

	result, err := client.Artifacts.Verify(context.Background(), 7)
	if err != nil {
		t.Fatalf("代验请求失败: %v", err)
	}
	if !result.Valid || !result.SignatureValid || result.ArtifactNo != "ART-2026-000007" {
		t.Fatalf("代验结果解析不符: %+v", result)
	}
	if !strings.Contains(string(hub.lastBody), `"id":7`) {
		t.Fatalf("代验请求体不符: %s", string(hub.lastBody))
	}
}

// TestArtifactVerifyWithFile - 发布物代验携带文件：multipart 表单（id 文本域 + file 文件域）
func TestArtifactVerifyWithFile(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/project-artifacts/verify"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {

		if err := request.ParseMultipartForm(32 << 20); err != nil {
			hub.writeError(writer, 400, "multipart 解析失败！", nil)
			return
		}
		if request.FormValue("id") != "7" {
			hub.writeError(writer, 400, "id 字段缺失！", nil)
			return
		}
		file, header, err := request.FormFile("file")
		if err != nil {
			hub.writeError(writer, 400, "file 字段缺失！", nil)
			return
		}
		defer func() { _ = file.Close() }()
		content, _ := io.ReadAll(file)
		if header.Filename != "app.tar.gz" || string(content) != "fake-binary" {
			hub.writeError(writer, 400, "文件内容不符！", nil)
			return
		}
		hub.writeData(writer, map[string]any{
			"id": 7, "artifactNo": "ART-2026-000007", "sha256": "abc",
			"recomputedSha256": "abc", "hashMatch": true, "signatureValid": true, "valid": true,
		})
	}
	client := hub.newClient(t)

	result, err := client.Artifacts.VerifyWithFile(context.Background(), 7, "app.tar.gz", strings.NewReader("fake-binary"))
	if err != nil {
		t.Fatalf("带文件代验失败: %v", err)
	}
	if !result.Valid || result.RecomputedSha256 != "abc" {
		t.Fatalf("带文件代验结果解析不符: %+v", result)
	}
}

// TestArtifactUpload - 发布物上传：multipart 表单字段与文件，返回发布物行
func TestArtifactUpload(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/project-artifacts/upload"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {

		if err := request.ParseMultipartForm(32 << 20); err != nil {
			hub.writeError(writer, 400, "multipart 解析失败！", nil)
			return
		}
		if request.FormValue("versionId") != "12" || request.FormValue("osArch") != "linux/amd64" {
			hub.writeError(writer, 400, "表单字段不符！", nil)
			return
		}
		file, _, err := request.FormFile("file")
		if err != nil {
			hub.writeError(writer, 400, "file 字段缺失！", nil)
			return
		}
		defer func() { _ = file.Close() }()
		_, _ = io.ReadAll(file)
		hub.writeData(writer, map[string]any{
			"id": 31, "artifactNo": "ART-2026-000031", "versionId": 12,
			"fileName": "app-linux-amd64.tar.gz", "sha256": "dead", "isLocked": true,
		})
	}
	client := hub.newClient(t)

	artifact, err := client.Artifacts.Upload(context.Background(), ArtifactUploadInput{
		VersionId: 12, OsArch: "linux/amd64",
	}, "app-linux-amd64.tar.gz", strings.NewReader("fake-binary"))
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if artifact.Id != 31 || artifact.ArtifactNo != "ART-2026-000031" || !artifact.IsLocked {
		t.Fatalf("上传结果解析不符: %+v", artifact)
	}
}

// TestQualificationCurrent - 我的资格状态（含最近一条申请的可空指针）
func TestQualificationCurrent(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/qualification/current"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"qualificationStatus": "approved", "projectQuota": 5, "defaultQuota": 3,
			"latestApplication": map[string]any{"id": 4, "applyNo": "QLF-2026-000004", "status": "approved"},
		})
	}
	client := hub.newClient(t)

	current, err := client.Qualification.Current(context.Background())
	if err != nil {
		t.Fatalf("资格状态请求失败: %v", err)
	}
	if current.QualificationStatus != "approved" || current.ProjectQuota != 5 || current.DefaultQuota != 3 {
		t.Fatalf("资格状态解析不符: %+v", current)
	}
	if current.LatestApplication == nil || current.LatestApplication.ApplyNo != "QLF-2026-000004" {
		t.Fatalf("最近申请解析不符: %+v", current.LatestApplication)
	}
}

// TestVersionRelease - 发布版本：POST {id}，返回 {id,version}
func TestVersionRelease(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/project-versions/release"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 12, "version": "1.4.0"})
	}
	client := hub.newClient(t)

	result, err := client.Versions.Release(context.Background(), 12)
	if err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	if result.Id != 12 || result.Version != "1.4.0" {
		t.Fatalf("发布结果解析不符: %+v", result)
	}
	if !strings.Contains(string(hub.lastBody), `"id":12`) {
		t.Fatalf("发布请求体不符: %s", string(hub.lastBody))
	}
}

// TestLicenseSeats - 许可证机器席位分页：query 透传 licenseId/status/分页，Page[LicenseSeat] 解析
func TestLicenseSeats(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/licenses/seats/find"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"data": []map[string]any{{
				"id": 3, "seatNo": "SEAT-2026-000003", "licenseId": 9,
				"fingerprintHash": "fp-hash-3", "deviceName": "dev-notebook",
				"status": "occupied", "currentActivationId": 31,
				"firstActivatedAt": 1780000000000, "lastSeenAt": 1780000001000,
			}},
			"count": 1, "page": 2,
		})
	}
	client := hub.newClient(t)

	page, err := client.Licenses.Seats(context.Background(), &LicenseSeatFindParams{
		LicenseId: 9, Status: "occupied", Page: 2, Limit: 10,
	})
	if err != nil {
		t.Fatalf("席位分页失败: %v", err)
	}
	if page.Count != 1 || len(page.Data) != 1 {
		t.Fatalf("席位分页解析不符: %+v", page)
	}
	seat := page.Data[0]
	if seat.SeatNo != "SEAT-2026-000003" || seat.DeviceName != "dev-notebook" || seat.Status != "occupied" {
		t.Fatalf("席位字段解析不符: %+v", seat)
	}
	query := hub.lastQuery
	if query.Get("licenseId") != "9" || query.Get("status") != "occupied" ||
		query.Get("page") != "2" || query.Get("limit") != "10" {
		t.Fatalf("席位查询参数不符: %s", query.Encode())
	}
}

// TestLicenseSeatTake - 机器席位详情：GET /api/licenses/seats/take?id=N
func TestLicenseSeatTake(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/licenses/seats/take"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"id": 3, "seatNo": "SEAT-2026-000003", "licenseId": 9,
			"fingerprintHash": "fp-hash-3", "deviceName": "dev-notebook",
			"status": "released", "releasedAt": 1780000002000, "releasedBy": 1,
			"releaseReason": "临时腾出席位",
		})
	}
	client := hub.newClient(t)

	seat, err := client.Licenses.SeatTake(context.Background(), 3)
	if err != nil {
		t.Fatalf("席位详情失败: %v", err)
	}
	if seat.SeatNo != "SEAT-2026-000003" || seat.Status != "released" || seat.ReleaseReason != "临时腾出席位" {
		t.Fatalf("席位详情解析不符: %+v", seat)
	}
	if hub.lastQuery.Get("id") != "3" {
		t.Fatalf("席位详情查询参数不符: %s", hub.lastQuery.Encode())
	}
}

// TestLicenseReleaseSeat - 释放机器席位：POST {id,reason}，返回 {id,status}
func TestLicenseReleaseSeat(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/licenses/seats/release"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 3, "status": "released"})
	}
	client := hub.newClient(t)

	result, err := client.Licenses.ReleaseSeat(context.Background(), LicenseSeatReleaseInput{
		Id: 3, Reason: "临时腾出席位",
	})
	if err != nil {
		t.Fatalf("释放席位失败: %v", err)
	}
	if result.Id != 3 || result.Status != "released" {
		t.Fatalf("释放结果解析不符: %+v", result)
	}
	var sent map[string]any
	if err = json.Unmarshal(hub.lastBody, &sent); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if sent["id"] != float64(3) || sent["reason"] != "临时腾出席位" {
		t.Fatalf("释放请求体不符: %s", string(hub.lastBody))
	}
}
