package licence

import (
	"context"
	"io"

	"github.com/spf13/cast"
)

// ArtifactsResource - 项目发布物资源（/api/project-artifacts/*）
type ArtifactsResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Rows - 发布物列表（不分页）：GET /api/project-artifacts/rows
func (this *ArtifactsResource) Rows(ctx context.Context, params *ArtifactFindParams) ([]ProjectArtifact, error) {

	var result []ProjectArtifact
	if err := this.client.get(ctx, "/api/project-artifacts/rows", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Find - 发布物分页：GET /api/project-artifacts/find
func (this *ArtifactsResource) Find(ctx context.Context, params *ArtifactFindParams) (*Page[ProjectArtifact], error) {

	var result Page[ProjectArtifact]
	if err := this.client.get(ctx, "/api/project-artifacts/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 发布物详情：GET /api/project-artifacts/take?id=N
func (this *ArtifactsResource) Take(ctx context.Context, id int) (*ProjectArtifact, error) {

	var result ProjectArtifact
	if err := this.client.getWithQuery(ctx, "/api/project-artifacts/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Upload - 上传发布物（multipart，文件字段 file；平台按上传字节算 SHA-256、
// release-key 签名并锁定，需 project.artifact.upload 权限）：POST /api/project-artifacts/upload
/**
 * @param input ArtifactUploadInput - 附加参数（VersionId 必填，版本须未发布/未归档）
 * @param fileName string - 原始文件名
 * @param content io.Reader - 文件内容
 * @example：
 * 	artifact, err := client.Artifacts.Upload(ctx, licence.ArtifactUploadInput{VersionId: 12}, "app-linux-amd64.tar.gz", file)
 */
func (this *ArtifactsResource) Upload(ctx context.Context, input ArtifactUploadInput, fileName string, content io.Reader) (*ProjectArtifact, error) {

	fields := map[string]string{
		"versionId":     cast.ToString(input.VersionId),
		"artifactType":  input.ArtifactType,
		"sourceVersion": input.SourceVersion,
		"targetVersion": input.TargetVersion,
		"osArch":        input.OsArch,
		"remark":        input.Remark,
	}
	var result ProjectArtifact
	if err := this.client.postMultipart(ctx, "/api/project-artifacts/upload", fields, "file", fileName, content, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Update - 更新发布物元数据（已锁定记录仅放行扫描状态）：PUT /api/project-artifacts/update
func (this *ArtifactsResource) Update(ctx context.Context, input ArtifactUpdateInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.put(ctx, "/api/project-artifacts/update", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Verify - 服务端代验发布物（按库内 sha256+signature 验签；登录即可用，数据范围按项目域约束）：
// POST /api/project-artifacts/verify
func (this *ArtifactsResource) Verify(ctx context.Context, id int) (*ArtifactVerifyResult, error) {

	var result ArtifactVerifyResult
	if err := this.client.post(ctx, "/api/project-artifacts/verify", map[string]any{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// VerifyWithFile - 服务端代验发布物并上传本地文件重算 SHA-256 双重校验（multipart，文件字段 file）：
// POST /api/project-artifacts/verify
func (this *ArtifactsResource) VerifyWithFile(ctx context.Context, id int, fileName string, content io.Reader) (*ArtifactVerifyResult, error) {

	var result ArtifactVerifyResult
	fields := map[string]string{"id": cast.ToString(id)}
	if err := this.client.postMultipart(ctx, "/api/project-artifacts/verify", fields, "file", fileName, content, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Remove - 逻辑删除（已签名锁定的发布物禁止删除）：DELETE /api/project-artifacts/remove
func (this *ArtifactsResource) Remove(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.del(ctx, "/api/project-artifacts/remove", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Delete - 物理删除（已签名锁定的发布物禁止删除）：DELETE /api/project-artifacts/delete
func (this *ArtifactsResource) Delete(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.del(ctx, "/api/project-artifacts/delete", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
