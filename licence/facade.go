package licence

import (
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	LicenceRuntime "github.com/inis-io/aide/licence/runtime"
)

// ============================= 协议常量与状态码（镜像 protocol 包） =============================

const (
	Algorithm               = LicenceProtocol.Algorithm
	BindingPolicySeats      = LicenceProtocol.BindingPolicySeats
	BindingPolicySingle     = LicenceProtocol.BindingPolicySingle
	EnvelopeVersion         = LicenceProtocol.EnvelopeVersion
	StatusClockTampered     = LicenceProtocol.StatusClockTampered
	StatusError             = LicenceProtocol.StatusError
	StatusExpired           = LicenceProtocol.StatusExpired
	StatusExpiring          = LicenceProtocol.StatusExpiring
	StatusFeatureNotAllowed = LicenceProtocol.StatusFeatureNotAllowed
	StatusGrace             = LicenceProtocol.StatusGrace
	StatusInstanceMismatch  = LicenceProtocol.StatusInstanceMismatch
	StatusLimitExceeded     = LicenceProtocol.StatusLimitExceeded
	StatusNotFound          = LicenceProtocol.StatusNotFound
	StatusRevoked           = LicenceProtocol.StatusRevoked
	StatusSeatLimitExceeded = LicenceProtocol.StatusSeatLimitExceeded
	StatusSeatReleased      = LicenceProtocol.StatusSeatReleased
	StatusSuspended         = LicenceProtocol.StatusSuspended
	StatusValid             = LicenceProtocol.StatusValid
	StatusVersionNotAllowed = LicenceProtocol.StatusVersionNotAllowed
)

// ============================= 信封、载荷与签发（镜像 protocol 包） =============================

type (
	Binding      = LicenceProtocol.Binding
	Envelope     = LicenceProtocol.Envelope
	LicenceClass = LicenceProtocol.LicenceClass
	Payload      = LicenceProtocol.Payload
)

var CompareVersion = LicenceProtocol.CompareVersion
var MarshalPayload = LicenceProtocol.MarshalPayload
var ParseEnvelope = LicenceProtocol.ParseEnvelope
var VersionInRange = LicenceProtocol.VersionInRange

var Licence = LicenceProtocol.Licence

// ============================= 客户端、传输与设备指纹（镜像 runtime 包） =============================

const (
	TransportGRPC = LicenceRuntime.TransportGRPC
	TransportHTTP = LicenceRuntime.TransportHTTP
)

type (
	Client              = LicenceRuntime.Client
	FingerprintProvider = LicenceRuntime.FingerprintProvider
	GRPCOptions         = LicenceRuntime.GRPCOptions
	Options             = LicenceRuntime.Options
	Store               = LicenceRuntime.Store
	SubscribedEvent     = LicenceRuntime.SubscribedEvent
	Transport           = LicenceRuntime.Transport
)

var FingerprintHash = LicenceRuntime.FingerprintHash
var LookupPublicKey = LicenceRuntime.LookupPublicKey
var New = LicenceRuntime.New
var NewGRPCConn = LicenceRuntime.NewGRPCConn

// ============================= SaaS 租户与菜单（镜像 runtime 包） =============================

type (
	SaasMenuImpactItem    = LicenceRuntime.SaasMenuImpactItem
	SaasMenuImpactReport  = LicenceRuntime.SaasMenuImpactReport
	SaasMenuWriteInput    = LicenceRuntime.SaasMenuWriteInput
	SaasMenuWriteResult   = LicenceRuntime.SaasMenuWriteResult
	SyncTenantMenusResult = LicenceRuntime.SyncTenantMenusResult
	TenantEnvelope        = LicenceRuntime.TenantEnvelope
	TenantInfo            = LicenceRuntime.TenantInfo
	TenantManifest        = LicenceRuntime.TenantManifest
	TenantManifests       = LicenceRuntime.TenantManifests
	TenantMenuSyncSkip    = LicenceRuntime.TenantMenuSyncSkip
	TenantPayload         = LicenceRuntime.TenantPayload
	TenantSearchItem      = LicenceRuntime.TenantSearchItem
	TenantValidateOptions = LicenceRuntime.TenantValidateOptions
)

var FilterManifestMenus = LicenceRuntime.FilterManifestMenus
var ParseTenantEnvelope = LicenceRuntime.ParseTenantEnvelope

// ============================= 平台配置同步（镜像 runtime 包） =============================

type (
	PlatformConfigEnvelope = LicenceRuntime.PlatformConfigEnvelope
	PlatformConfigGroup    = LicenceRuntime.PlatformConfigGroup
	PlatformConfigItem     = LicenceRuntime.PlatformConfigItem
	PlatformConfigPayload  = LicenceRuntime.PlatformConfigPayload
)

var ParsePlatformConfigEnvelope = LicenceRuntime.ParsePlatformConfigEnvelope

// ============================= 在线更新与发布清单（镜像 runtime 包） =============================

const (
	UpgradeDownloading = LicenceRuntime.UpgradeDownloading
	UpgradeFailed      = LicenceRuntime.UpgradeFailed
	UpgradeInstalling  = LicenceRuntime.UpgradeInstalling
	UpgradePending     = LicenceRuntime.UpgradePending
	UpgradeRolledBack  = LicenceRuntime.UpgradeRolledBack
	UpgradeSuccess     = LicenceRuntime.UpgradeSuccess
)

type (
	ArtifactPayload      = LicenceRuntime.ArtifactPayload
	Manifest             = LicenceRuntime.Manifest
	ManifestArtifact     = LicenceRuntime.ManifestArtifact
	ManifestMenu         = LicenceRuntime.ManifestMenu
	ManifestMenuRoute    = LicenceRuntime.ManifestMenuRoute
	ManifestPayload      = LicenceRuntime.ManifestPayload
	ManifestUpdatePolicy = LicenceRuntime.ManifestUpdatePolicy
	UpdateInfo           = LicenceRuntime.UpdateInfo
	UpgradeReport        = LicenceRuntime.UpgradeReport
)

var MarshalManifestPayload = LicenceRuntime.MarshalManifestPayload
var ParseManifest = LicenceRuntime.ParseManifest

// ============================= 自助发放与兑换（镜像 runtime 包） =============================

type (
	ProvisionError   = LicenceRuntime.ProvisionError
	ProvisionOptions = LicenceRuntime.ProvisionOptions
	ProvisionResult  = LicenceRuntime.ProvisionResult
	RedeemOptions    = LicenceRuntime.RedeemOptions
)

var Provision = LicenceRuntime.Provision
var Redeem = LicenceRuntime.Redeem

// ============================= 配置回推与定义反推（镜像 runtime 包） =============================

type (
	PushbackDefinitionsError  = LicenceRuntime.PushbackDefinitionsError
	PushbackDefinitionsResult = LicenceRuntime.PushbackDefinitionsResult
	PushbackResult            = LicenceRuntime.PushbackResult
)

// ============================= 星链中枢（Star Hub，镜像 runtime 包） =============================

const (
	StarStatusActive     = LicenceRuntime.StarStatusActive
	StarStatusSuspended  = LicenceRuntime.StarStatusSuspended
	StarStatusLeft       = LicenceRuntime.StarStatusLeft
	StarStatsAccepted    = LicenceRuntime.StarStatsAccepted
	StarStatsQuarantined = LicenceRuntime.StarStatsQuarantined
	StarOutcomePending   = LicenceRuntime.StarOutcomePending
	StarOutcomeUpheld    = LicenceRuntime.StarOutcomeUpheld
	StarOutcomeRejected  = LicenceRuntime.StarOutcomeRejected
)

var ErrStarNotActivated = LicenceRuntime.ErrStarNotActivated

type (
	StarService                  = LicenceRuntime.StarService
	StarJoinInput                = LicenceRuntime.StarJoinInput
	StarJoinResult               = LicenceRuntime.StarJoinResult
	StarPeer                     = LicenceRuntime.StarPeer
	StarPeersResult              = LicenceRuntime.StarPeersResult
	StarHotIndexItem             = LicenceRuntime.StarHotIndexItem
	StarPushHotIndexInput        = LicenceRuntime.StarPushHotIndexInput
	StarPullGlobalHotInput       = LicenceRuntime.StarPullGlobalHotInput
	StarGlobalHotItem            = LicenceRuntime.StarGlobalHotItem
	StarCreditPurchaseInput      = LicenceRuntime.StarCreditPurchaseInput
	StarCreditPurchaseResult     = LicenceRuntime.StarCreditPurchaseResult
	StarGrantOutInput            = LicenceRuntime.StarGrantOutInput
	StarGrantOutResult           = LicenceRuntime.StarGrantOutResult
	StarPromoteOrderCreateInput  = LicenceRuntime.StarPromoteOrderCreateInput
	StarPromoteOrderCreateResult = LicenceRuntime.StarPromoteOrderCreateResult
	StarPromoteOrderSettleInput  = LicenceRuntime.StarPromoteOrderSettleInput
	StarSettleBill               = LicenceRuntime.StarSettleBill
	StarSettleTargetLine         = LicenceRuntime.StarSettleTargetLine
	StarPromoteOrderCancelInput  = LicenceRuntime.StarPromoteOrderCancelInput
	StarPromoteOrderCancelResult = LicenceRuntime.StarPromoteOrderCancelResult
	StarReportPromoteStatsInput  = LicenceRuntime.StarReportPromoteStatsInput
	StarReportOutcomeInput       = LicenceRuntime.StarReportOutcomeInput
)
