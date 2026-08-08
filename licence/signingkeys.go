package licence

import "context"

// SigningKeysResource - 签名密钥资源（/api/signing-keys/*）
// 只导出公钥与密钥版本，平台任何接口不返回私钥种子。
type SigningKeysResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Public - 导出公钥（任意登录用户可读）：GET /api/signing-keys/public?purpose=license|release&keyVersion=
// purpose 仅支持 license（许可证验签）与 release（发布物验签）；
// keyVersion 留空取当前版本，release 用途支持按版本导出历史公钥，license 仅保留当前版本。
func (this *SigningKeysResource) Public(ctx context.Context, purpose string, keyVersion string) (*SigningKeyPublic, error) {

	var result SigningKeyPublic
	if err := this.client.get(ctx, "/api/signing-keys/public", map[string]string{
		"purpose": purpose, "keyVersion": keyVersion,
	}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Rotate - 轮换签名密钥（高风险操作，需 system.signing-key.rotate 权限）：POST /api/signing-keys/rotate
// release 保留历史版本供旧发布物验签；license 仅切换当前版本。
func (this *SigningKeysResource) Rotate(ctx context.Context, purpose string) (*SigningKeyPublic, error) {

	var result SigningKeyPublic
	if err := this.client.post(ctx, "/api/signing-keys/rotate", map[string]string{
		"purpose": purpose,
	}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
