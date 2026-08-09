$ErrorActionPreference = "Stop"

$protoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$moduleRoot = Split-Path -Parent $protoRoot

Push-Location $moduleRoot
try {
    protoc `
        --proto_path=proto `
        --go_out=proto --go_opt=paths=source_relative `
        --go-grpc_out=proto --go-grpc_opt=paths=source_relative `
        proto/licence/v1/runtime.proto `
        proto/licence/v1/admin.proto
} finally {
    Pop-Location
}
