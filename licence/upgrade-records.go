package licence

import "context"

// UpgradeRecordsResource - 升级执行记录资源（/api/project-upgrade-records/*，只读）。
// 记录由运行面 updates/report|logs 写入，管理面仅提供查询观测（灰度效果与失败原因追溯）。
type UpgradeRecordsResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Rows - 升级记录列表（不分页）：GET /api/project-upgrade-records/rows
func (this *UpgradeRecordsResource) Rows(ctx context.Context, params *UpgradeRecordFindParams) ([]UpgradeRecord, error) {

	var result []UpgradeRecord
	if err := this.client.get(ctx, "/api/project-upgrade-records/rows", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Find - 升级记录分页：GET /api/project-upgrade-records/find
func (this *UpgradeRecordsResource) Find(ctx context.Context, params *UpgradeRecordFindParams) (*Page[UpgradeRecord], error) {

	var result Page[UpgradeRecord]
	if err := this.client.get(ctx, "/api/project-upgrade-records/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 升级记录详情（含过程日志）：GET /api/project-upgrade-records/take?id=N
func (this *UpgradeRecordsResource) Take(ctx context.Context, id int) (*UpgradeRecord, error) {

	var result UpgradeRecord
	if err := this.client.getWithQuery(ctx, "/api/project-upgrade-records/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}
